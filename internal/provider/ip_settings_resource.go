package provider

import (
	"context"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jasonobrien/terraform-provider-sks3200/internal/sks3200"
)

// ipSettingsResource is a singleton: the switch's IPv4 management address.
type ipSettingsResource struct {
	client *client
}

func newIPSettingsResource() resource.Resource {
	return &ipSettingsResource{}
}

type ipSettingsModel struct {
	ID          types.String `tfsdk:"id"`
	DHCPEnabled types.Bool   `tfsdk:"dhcp_enabled"`
	IPAddress   types.String `tfsdk:"ip_address"`
	Netmask     types.String `tfsdk:"netmask"`
	Gateway     types.String `tfsdk:"gateway"`
}

func (r *ipSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ip_address_settings"
}

func (r *ipSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "IPv4 management address. Destroying it leaves the switch as is. " +
			"Changing the address moves the switch away from the provider's url; update url to match.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   `Always "network".`,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"dhcp_enabled": schema.BoolAttribute{
				Description: "Get the address by DHCP; ip_address, netmask and gateway are then not sent to the switch. Defaults to false.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"ip_address": schema.StringAttribute{
				Description: "IPv4 address, e.g. 192.168.10.12. Keeps the switch's current value if unset.",
				Optional:    true,
				Computed:    true,
			},
			"netmask": schema.StringAttribute{
				Description: "Dotted-quad netmask, e.g. 255.255.255.0. Keeps the switch's current value if unset.",
				Optional:    true,
				Computed:    true,
			},
			"gateway": schema.StringAttribute{
				Description: "Default gateway. Keeps the switch's current value if unset.",
				Optional:    true,
				Computed:    true,
			},
		},
	}
}

func (r *ipSettingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (r *ipSettingsResource) apply(ctx context.Context, plan *ipSettingsModel) (moved bool, err error) {
	n := sks3200.Network{
		DHCP:      plan.DHCPEnabled.ValueBool(),
		IPAddress: plan.IPAddress.ValueString(),
		Netmask:   plan.Netmask.ValueString(),
		Gateway:   plan.Gateway.ValueString(),
	}
	cur, err := r.client.Network(ctx)
	if err != nil {
		return false, err
	}
	// Optional fields left out of the config keep the switch's values.
	if plan.IPAddress.IsUnknown() {
		n.IPAddress = cur.IPAddress
	}
	if plan.Netmask.IsUnknown() {
		n.Netmask = cur.Netmask
	}
	if plan.Gateway.IsUnknown() {
		n.Gateway = cur.Gateway
	}
	plan.ID = types.StringValue("network")
	plan.IPAddress = types.StringValue(n.IPAddress)
	plan.Netmask = types.StringValue(n.Netmask)
	plan.Gateway = types.StringValue(n.Gateway)
	if *cur == n {
		return false, nil
	}
	if err := r.client.SetNetwork(ctx, n); err != nil {
		return false, err
	}
	u, _ := url.Parse(r.client.url)
	return n.DHCP || u == nil || u.Hostname() != n.IPAddress, nil
}

// set applies plan and returns the state to store.
func (r *ipSettingsResource) set(ctx context.Context, plan ipSettingsModel, diags *diag.Diagnostics) ipSettingsModel {
	moved, err := r.apply(ctx, &plan)
	if err != nil {
		diags.AddError("Setting IP address", err.Error())
		return plan
	}
	if moved {
		diags.AddWarning("Switch address changed",
			"The switch is no longer at "+r.client.url+" and its config was not saved; "+
				"point the provider url at the new address and apply again to save it.")
	} else {
		r.client.commit(ctx, diags)
	}
	return plan
}

func (r *ipSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ipSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state := r.set(ctx, plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	}
}

func (r *ipSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ipSettingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state := r.set(ctx, plan, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	}
}

func (r *ipSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ipSettingsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	n, err := r.client.Network(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading IP address", err.Error())
		return
	}
	state.ID = types.StringValue("network")
	state.DHCPEnabled = types.BoolValue(n.DHCP)
	state.IPAddress = types.StringValue(n.IPAddress)
	state.Netmask = types.StringValue(n.Netmask)
	state.Gateway = types.StringValue(n.Gateway)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *ipSettingsResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *ipSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
