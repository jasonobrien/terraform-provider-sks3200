package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jasonobrien/terraform-provider-sks3200/internal/sks3200"
)

var frameTypes = map[string]sks3200.FrameType{
	"all":      sks3200.AllFrames,
	"tagged":   sks3200.TaggedFrames,
	"untagged": sks3200.UntaggedFrames,
}

func frameTypeName(f sks3200.FrameType) string {
	for k, v := range frameTypes {
		if v == f {
			return k
		}
	}
	return strconv.Itoa(int(f))
}

type portVLANResource struct {
	client *client
}

func newPortVLANResource() resource.Resource {
	return &portVLANResource{}
}

type portVLANModel struct {
	ID             types.String `tfsdk:"id"`
	Port           types.Int64  `tfsdk:"port"`
	PVID           types.Int64  `tfsdk:"pvid"`
	AcceptedFrames types.String `tfsdk:"accepted_frames"`
}

func (r *portVLANResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_port_vlan"
}

func (r *portVLANResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A port's PVID (the VLAN for untagged ingress) and accepted frame types. " +
			"Destroying it resets the port to PVID 1, all frames.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"port": schema.Int64Attribute{
				Description:   "Port number, 1-based.",
				Required:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
			},
			"pvid": schema.Int64Attribute{
				Required:   true,
				Validators: []validator.Int64{int64validator.Between(1, 4094)},
			},
			"accepted_frames": schema.StringAttribute{
				Description: `One of "all" (default), "tagged" or "untagged".`,
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("all"),
				Validators:  []validator.String{stringvalidator.OneOf("all", "tagged", "untagged")},
			},
		},
	}
}

func (r *portVLANResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *portVLANResource) set(ctx context.Context, p sks3200.PortVLAN, diags *diag.Diagnostics) {
	if err := r.client.SetPortVLAN(ctx, p); err != nil {
		diags.AddError(fmt.Sprintf("Setting port %d VLAN", p.Port), err.Error())
		return
	}
	r.client.commit(ctx, diags)
}

func (r *portVLANResource) apply(ctx context.Context, plan *portVLANModel, diags *diag.Diagnostics) {
	r.set(ctx, sks3200.PortVLAN{
		Port:      int(plan.Port.ValueInt64()),
		PVID:      int(plan.PVID.ValueInt64()),
		FrameType: frameTypes[plan.AcceptedFrames.ValueString()],
	}, diags)
	plan.ID = types.StringValue(strconv.FormatInt(plan.Port.ValueInt64(), 10))
}

func (r *portVLANResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan portVLANModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *portVLANResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan portVLANModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	}
}

func (r *portVLANResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state portVLANModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ports, err := r.client.PortVLANs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading port VLANs", err.Error())
		return
	}
	port := int(state.Port.ValueInt64())
	if port < 1 || port > len(ports) {
		resp.State.RemoveResource(ctx)
		return
	}
	p := ports[port-1]
	state.ID = types.StringValue(strconv.Itoa(port))
	state.PVID = types.Int64Value(int64(p.PVID))
	state.AcceptedFrames = types.StringValue(frameTypeName(p.FrameType))
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *portVLANResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state portVLANModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.set(ctx, sks3200.PortVLAN{Port: int(state.Port.ValueInt64()), PVID: 1}, &resp.Diagnostics)
}

func (r *portVLANResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	port, err := strconv.Atoi(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", "Expected a port number, e.g. 2.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("port"), int64(port))...)
}
