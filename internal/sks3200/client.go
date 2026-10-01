// Package sks3200 is a client for the JSON web API of XikeStor SKS3200-8E2X
// switches on firmware 2.0.x. The API is undocumented; endpoints and body
// shapes come from the switch's own web UI, and from
// https://github.com/planetf1/sks32kmon (docs/API.md).
package sks3200

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client talks to one switch. The switch handles one request at a time
// poorly, so all calls are serialized.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client

	mu       sync.Mutex
	loggedIn bool
}

func New(baseURL, username, password string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		username: username,
		password: password,
		http:     &http.Client{Jar: jar, Timeout: 30 * time.Second},
	}, nil
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// expired reports whether the switch answered with its login redirect. It
// does so with HTTP 200, as JSON ({"redirect": ".../login.html"}) or as a
// <script> redirect, depending on the endpoint.
func expired(body []byte) bool {
	return bytes.Contains(body, []byte("login.html"))
}

func (c *Client) login(ctx context.Context) error {
	q := url.Values{
		"loginusr": {md5hex(c.username)},
		"loginpwd": {md5hex(c.password)},
	}
	body, err := c.send(ctx, http.MethodGet, "/authorize?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if expired(body) {
		return fmt.Errorf("login: rejected; check username and password")
	}
	c.loggedIn = true
	return nil
}

func (c *Client) send(ctx context.Context, method, path string, payload any) ([]byte, error) {
	var r io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s: HTTP %d", method, path, resp.StatusCode)
	}
	return body, nil
}

// do sends a request, logging in first and once more if the session expired.
func (c *Client) do(ctx context.Context, method, path string, payload any) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for attempt := 0; ; attempt++ {
		if !c.loggedIn {
			if err := c.login(ctx); err != nil {
				return nil, err
			}
		}
		body, err := c.send(ctx, method, path, payload)
		if err != nil {
			return nil, err
		}
		if !expired(body) {
			return body, nil
		}
		c.loggedIn = false
		if attempt > 0 {
			return nil, fmt.Errorf("%s %s: session rejected after login", method, path)
		}
	}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	body, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	// Decode only the first value: some responses carry trailing junk.
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(out); err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	return nil
}

// post sends a write. Write endpoints answer with an empty body on success.
func (c *Client) post(ctx context.Context, path string, payload any) error {
	body, err := c.do(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}
	if b := bytes.TrimSpace(body); len(b) > 0 {
		return fmt.Errorf("POST %s: switch refused: %s", path, b)
	}
	return nil
}

// Save persists the running config; unsaved changes are lost on reboot.
func (c *Client) Save(ctx context.Context) error {
	return c.post(ctx, "/save_all_configs.json", struct{}{})
}

type Status struct {
	Temperature string `json:"temperature"`
	IPAddress   string `json:"sys_ipv4"`
	MACAddress  string `json:"sys_macaddr"`
	Firmware    string `json:"fw_ver"`
	Hardware    string `json:"hw_ver"`
	Model       string `json:"des"`
}

func (c *Client) Status(ctx context.Context) (*Status, error) {
	var s Status
	return &s, c.get(ctx, "/status.json", &s)
}

type Network struct {
	DHCP      bool
	IPAddress string
	Netmask   string
	Gateway   string
}

func (c *Client) Network(ctx context.Context) (*Network, error) {
	var raw struct {
		IPAddress   string `json:"ipAddress"`
		Netmask     string `json:"netmask"`
		Gateway     string `json:"gateway"`
		DHCPEnabled string `json:"dhcpEnabled"`
	}
	if err := c.get(ctx, "/network_settings.json", &raw); err != nil {
		return nil, err
	}
	return &Network{
		DHCP:      raw.DHCPEnabled == "1",
		IPAddress: raw.IPAddress,
		Netmask:   raw.Netmask,
		Gateway:   raw.Gateway,
	}, nil
}

// SetNetwork changes the management address. The switch moves to the new
// address immediately, so this client is unusable afterwards if it changed.
func (c *Client) SetNetwork(ctx context.Context, n Network) error {
	body := map[string]string{"dhcp_enable": "0"}
	if n.DHCP {
		body["dhcp_enable"] = "1"
	} else {
		body["input_ip"] = n.IPAddress
		body["input_netmask"] = n.Netmask
		body["input_gateway"] = n.Gateway
	}
	b, err := c.do(ctx, http.MethodPost, "/network_settings_ipv4.json", body)
	if err != nil {
		return err
	}
	// The web UI expects {"alert_key": "alert_success"}; 2.0.0.3 sends nothing.
	if t := bytes.TrimSpace(b); len(t) > 0 && !bytes.Contains(t, []byte("alert_success")) {
		return fmt.Errorf("POST /network_settings_ipv4.json: switch refused: %s", t)
	}
	return nil
}

// PortState is a port's membership in an 802.1Q VLAN.
type PortState int

const (
	NotMember PortState = 0
	Untagged  PortState = 1
	Tagged    PortState = 2
)

type VLAN struct {
	ID   int
	Name string
	// Ports[i] is the state of port i+1.
	Ports []PortState
}

// VLANs lists the 802.1Q VLANs. The switch serves them as a short
// server-sent event stream: a {"PortNum": n} event, then one per VLAN.
func (c *Client) VLANs(ctx context.Context) ([]VLAN, error) {
	body, err := c.do(ctx, http.MethodGet, "/tag_vlan.json", nil)
	if err != nil {
		return nil, err
	}
	var vlans []VLAN
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		var ev struct {
			VLANID     *int        `json:"vlan_id"`
			VLANName   string      `json:"vlan_name"`
			PortStates []PortState `json:"port_states"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &ev); err != nil {
			return nil, fmt.Errorf("GET /tag_vlan.json: %w", err)
		}
		if ev.VLANID != nil {
			vlans = append(vlans, VLAN{ID: *ev.VLANID, Name: ev.VLANName, Ports: ev.PortStates})
		}
	}
	return vlans, sc.Err()
}

func (c *Client) VLAN(ctx context.Context, id int) (*VLAN, error) {
	vlans, err := c.VLANs(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range vlans {
		if v.ID == id {
			return &v, nil
		}
	}
	return nil, nil
}

// SetVLAN creates or replaces a VLAN. The switch refuses to drop a port from
// the VLAN that is its PVID.
func (c *Client) SetVLAN(ctx context.Context, v VLAN) error {
	// Unlike the GET, the POST indexes ports from 1; slot 0 is unused.
	states := append([]PortState{NotMember}, v.Ports...)
	return c.post(ctx, "/tag_vlan.json", map[string]any{
		"deletedVlans": []int{},
		"updatedVlans": []map[string]any{{
			"vlan_id":     strconv.Itoa(v.ID),
			"vlan_name":   v.Name,
			"port_states": states,
		}},
	})
}

func (c *Client) DeleteVLAN(ctx context.Context, id int) error {
	return c.post(ctx, "/tag_vlan.json", map[string]any{
		"deletedVlans": []int{id},
		"updatedVlans": []any{},
	})
}

// FrameType selects which frames a port accepts on ingress.
type FrameType int

const (
	AllFrames      FrameType = 0
	TaggedFrames   FrameType = 1
	UntaggedFrames FrameType = 2
)

type PortVLAN struct {
	Port      int
	PVID      int
	FrameType FrameType
}

func (c *Client) PortVLANs(ctx context.Context) ([]PortVLAN, error) {
	var raw map[string]json.RawMessage
	if err := c.get(ctx, "/port_vlan.json", &raw); err != nil {
		return nil, err
	}
	var n int
	if err := json.Unmarshal(raw["PortNum"], &n); err != nil {
		return nil, fmt.Errorf("GET /port_vlan.json: PortNum: %w", err)
	}
	ports := make([]PortVLAN, 0, n)
	for i := 1; i <= n; i++ {
		var p struct {
			PVID      int       `json:"PVID"`
			FrameType FrameType `json:"Frame_Type"`
		}
		if err := json.Unmarshal(raw[fmt.Sprintf("Port_%d", i)], &p); err != nil {
			return nil, fmt.Errorf("GET /port_vlan.json: Port_%d: %w", i, err)
		}
		ports = append(ports, PortVLAN{Port: i, PVID: p.PVID, FrameType: p.FrameType})
	}
	return ports, nil
}

func (c *Client) SetPortVLAN(ctx context.Context, p PortVLAN) error {
	return c.post(ctx, "/port_vlan.json", map[string]any{
		"port_based_vlan_frame_type": strconv.Itoa(int(p.FrameType)),
		"port_based_vlan_pvid_input": strconv.Itoa(p.PVID),
		"port_based_vlan_selection":  []string{strconv.Itoa(p.Port)},
	})
}
