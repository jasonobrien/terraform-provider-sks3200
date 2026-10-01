package provider

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jasonobrien/terraform-provider-sks3200/internal/sks3200"
)

type vlanResource struct {
	client *client
}

func newVLANResource() resource.Resource {
	return &vlanResource{}
}

type vlanModel struct {
	ID            types.String `tfsdk:"id"`
	VLANID        types.Int64  `tfsdk:"vlan_id"`
	Name          types.String `tfsdk:"name"`
	UntaggedPorts types.Set    `tfsdk:"untagged_ports"`
	TaggedPorts   types.Set    `tfsdk:"tagged_ports"`
}

func (r *vlanResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vlan_8021q"
}

func portSet() schema.SetAttribute {
	return schema.SetAttribute{
		ElementType: types.Int64Type,
		Optional:    true,
		Computed:    true,
		Default:     setdefault.StaticValue(types.SetValueMust(types.Int64Type, nil)),
		Validators: []validator.Set{
			setvalidator.ValueInt64sAre(int64validator.AtLeast(1)),
		},
	}
}

func (r *vlanResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	untagged, tagged := portSet(), portSet()
	untagged.Description = "Ports (1-based) that send this VLAN's frames untagged."
	tagged.Description = "Ports (1-based) that send this VLAN's frames tagged."
	resp.Schema = schema.Schema{
		Description: "An 802.1Q VLAN and its member ports. The switch refuses to remove a port " +
			"from its PVID's VLAN; change the port's sks3200_port_vlan first. A port is untagged in " +
			"at most one VLAN: adding it here silently removes it from the VLAN it was untagged in. " +
			"VLAN 1 can't be deleted: import it, and destroying it only drops it from state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"vlan_id": schema.Int64Attribute{
				Required:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				Validators:    []validator.Int64{int64validator.Between(1, 4094)},
			},
			"name": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				Default:    stringdefault.StaticString(""),
				Validators: []validator.String{stringvalidator.LengthAtMost(16)},
			},
			"untagged_ports": untagged,
			"tagged_ports":   tagged,
		},
	}
}

func (r *vlanResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *vlanResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m vlanModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.UntaggedPorts.IsUnknown() || m.TaggedPorts.IsUnknown() {
		return
	}
	untagged := portsFromSet(ctx, m.UntaggedPorts, &resp.Diagnostics)
	for _, p := range portsFromSet(ctx, m.TaggedPorts, &resp.Diagnostics) {
		if contains(untagged, p) {
			resp.Diagnostics.AddAttributeError(path.Root("tagged_ports"), "Port in both sets",
				fmt.Sprintf("Port %d is listed as both tagged and untagged.", p))
		}
	}
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func portsFromSet(ctx context.Context, s types.Set, diags *diag.Diagnostics) []int {
	var ports []int64
	diags.Append(s.ElementsAs(ctx, &ports, false)...)
	out := make([]int, len(ports))
	for i, p := range ports {
		out[i] = int(p)
	}
	sort.Ints(out)
	return out
}

func setFromPorts(ports []int) types.Set {
	elems := make([]attr.Value, len(ports))
	for i, p := range ports {
		elems[i] = types.Int64Value(int64(p))
	}
	return types.SetValueMust(types.Int64Type, elems)
}

// numPorts reads the port count from the switch's default VLAN.
func (r *vlanResource) numPorts(ctx context.Context) (int, error) {
	vlans, err := r.client.VLANs(ctx)
	if err != nil {
		return 0, err
	}
	if len(vlans) == 0 {
		return 0, fmt.Errorf("switch reported no VLANs")
	}
	return len(vlans[0].Ports), nil
}

func (r *vlanResource) toVLAN(ctx context.Context, m vlanModel, diags *diag.Diagnostics) *sks3200.VLAN {
	n, err := r.numPorts(ctx)
	if err != nil {
		diags.AddError("Reading VLANs", err.Error())
		return nil
	}
	v := &sks3200.VLAN{
		ID:    int(m.VLANID.ValueInt64()),
		Name:  m.Name.ValueString(),
		Ports: make([]sks3200.PortState, n),
	}
	assign := func(attr string, s types.Set, state sks3200.PortState) {
		for _, p := range portsFromSet(ctx, s, diags) {
			if p > n {
				diags.AddAttributeError(path.Root(attr), "No such port",
					fmt.Sprintf("Port %d is out of range; the switch has %d.", p, n))
				continue
			}
			v.Ports[p-1] = state
		}
	}
	assign("untagged_ports", m.UntaggedPorts, sks3200.Untagged)
	assign("tagged_ports", m.TaggedPorts, sks3200.Tagged)
	if diags.HasError() {
		return nil
	}
	return v
}

func fromVLAN(v *sks3200.VLAN) vlanModel {
	var untagged, tagged []int
	for i, s := range v.Ports {
		switch s {
		case sks3200.Untagged:
			untagged = append(untagged, i+1)
		case sks3200.Tagged:
			tagged = append(tagged, i+1)
		}
	}
	return vlanModel{
		ID:            types.StringValue(strconv.Itoa(v.ID)),
		VLANID:        types.Int64Value(int64(v.ID)),
		Name:          types.StringValue(v.Name),
		UntaggedPorts: setFromPorts(untagged),
		TaggedPorts:   setFromPorts(tagged),
	}
}

func (r *vlanResource) set(ctx context.Context, plan vlanModel, diags *diag.Diagnostics) {
	v := r.toVLAN(ctx, plan, diags)
	if v == nil {
		return
	}
	if err := r.client.SetVLAN(ctx, *v); err != nil {
		diags.AddError(fmt.Sprintf("Setting VLAN %d", v.ID), err.Error())
		return
	}
	r.client.commit(ctx, diags)
}

func (r *vlanResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vlanModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := int(plan.VLANID.ValueInt64())
	existing, err := r.client.VLAN(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Reading VLANs", err.Error())
		return
	}
	if existing != nil {
		resp.Diagnostics.AddError("VLAN already exists",
			fmt.Sprintf("VLAN %d already exists on the switch; import it instead.", id))
		return
	}
	r.set(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = types.StringValue(strconv.Itoa(id))
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *vlanResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vlanModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := r.client.VLAN(ctx, int(state.VLANID.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Reading VLANs", err.Error())
		return
	}
	if v == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, fromVLAN(v))...)
}

func (r *vlanResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan vlanModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.set(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = types.StringValue(strconv.FormatInt(plan.VLANID.ValueInt64(), 10))
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *vlanResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vlanModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := int(state.VLANID.ValueInt64())
	if id == 1 {
		return
	}
	if err := r.client.DeleteVLAN(ctx, id); err != nil {
		resp.Diagnostics.AddError(fmt.Sprintf("Deleting VLAN %d", id), err.Error())
		return
	}
	r.client.commit(ctx, &resp.Diagnostics)
}

func (r *vlanResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := strconv.Atoi(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", "Expected a VLAN ID, e.g. 1.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("vlan_id"), int64(id))...)
}
