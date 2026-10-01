package sks3200

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// fakeSwitch mimics firmware 2.0.0.3: login redirects with HTTP 200 and
// trailing junk after JSON bodies.
type fakeSwitch struct {
	session string
	logins  int
	posts   map[string][]byte
}

func newFake(t *testing.T) (*fakeSwitch, *Client) {
	f := &fakeSwitch{posts: map[string][]byte{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL+"/", "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func (f *fakeSwitch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/authorize" {
		q := r.URL.Query()
		if q.Get("loginusr") != md5hex("admin") || q.Get("loginpwd") != md5hex("secret") {
			fmt.Fprint(w, "<script>window.location.href = 'http://x/login.html';</script>")
			return
		}
		f.logins++
		f.session = fmt.Sprint("s", f.logins)
		http.SetCookie(w, &http.Cookie{Name: "session", Value: f.session})
		fmt.Fprint(w, "http://x/index.html?page=")
		return
	}
	if ck, err := r.Cookie("session"); err != nil || ck.Value != f.session {
		fmt.Fprint(w, `{"redirect":"http://x:80/login.html"}HTTP/1.1 200 OK`)
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /status.json":
		fmt.Fprint(w, `{"fw_ver":"2.0.0.3","des":"SKS3200-8E2X"}`+"\nHTTP/1.1 200 OK\n")
	case "GET /tag_vlan.json":
		fmt.Fprint(w, "data: {\"PortNum\":3}\n\n"+
			"data: {\"vlan_id\":1,\"vlan_name\":\"\",\"port_states\":[1,1,0],\"port_pvids\":[1,1,1]}\n\n"+
			"data: {\"vlan_id\":20,\"vlan_name\":\"guest\",\"port_states\":[0,2,1],\"port_pvids\":[1,1,1]}\n\n")
	case "GET /port_vlan.json":
		fmt.Fprint(w, `{"PortNum":2,"Port_1":{"Port_Id":1,"PVID":1,"Frame_Type":0},"Port_2":{"Port_Id":2,"PVID":20,"Frame_Type":1}}`)
	case "POST /tag_vlan.json", "POST /port_vlan.json", "POST /save_all_configs.json":
		f.posts[r.URL.Path], _ = io.ReadAll(r.Body)
	case "POST /network_settings_ipv4.json":
		fmt.Fprint(w, `{"alert_key":"alert_fail"}`)
	default:
		http.Error(w, "Bad Request", http.StatusBadRequest)
	}
}

func TestRelogin(t *testing.T) {
	f, c := newFake(t)
	ctx := context.Background()
	if _, err := c.Status(ctx); err != nil {
		t.Fatal(err)
	}
	f.session = "expired"
	s, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Firmware != "2.0.0.3" || f.logins != 2 {
		t.Errorf("got %+v after %d logins", s, f.logins)
	}
}

func TestBadPassword(t *testing.T) {
	_, c := newFake(t)
	c.password = "wrong"
	if _, err := c.Status(context.Background()); err == nil {
		t.Fatal("expected login error")
	}
}

func TestVLANs(t *testing.T) {
	_, c := newFake(t)
	got, err := c.VLANs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []VLAN{
		{ID: 1, Name: "", Ports: []PortState{Untagged, Untagged, NotMember}},
		{ID: 20, Name: "guest", Ports: []PortState{NotMember, Tagged, Untagged}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSetVLAN(t *testing.T) {
	f, c := newFake(t)
	err := c.SetVLAN(context.Background(), VLAN{ID: 20, Name: "guest", Ports: []PortState{NotMember, Tagged, Untagged}})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Updated []struct {
			ID     string `json:"vlan_id"`
			States []int  `json:"port_states"`
		} `json:"updatedVlans"`
	}
	if err := json.Unmarshal(f.posts["/tag_vlan.json"], &body); err != nil {
		t.Fatal(err)
	}
	// Slot 0 is padding; ports start at index 1.
	if u := body.Updated; len(u) != 1 || u[0].ID != "20" || !reflect.DeepEqual(u[0].States, []int{0, 0, 2, 1}) {
		t.Errorf("posted %s", f.posts["/tag_vlan.json"])
	}
}

func TestPortVLANs(t *testing.T) {
	f, c := newFake(t)
	ctx := context.Background()
	got, err := c.PortVLANs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []PortVLAN{{1, 1, AllFrames}, {2, 20, TaggedFrames}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if err := c.SetPortVLAN(ctx, PortVLAN{Port: 2, PVID: 30, FrameType: UntaggedFrames}); err != nil {
		t.Fatal(err)
	}
	const wantBody = `{"port_based_vlan_frame_type":"2","port_based_vlan_pvid_input":"30","port_based_vlan_selection":["2"]}`
	if got := string(f.posts["/port_vlan.json"]); got != wantBody {
		t.Errorf("posted %s", got)
	}
}

func TestSetNetworkRefused(t *testing.T) {
	_, c := newFake(t)
	if err := c.SetNetwork(context.Background(), Network{IPAddress: "10.0.0.2"}); err == nil {
		t.Fatal("expected refusal")
	}
}
