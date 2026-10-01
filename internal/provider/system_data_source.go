package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type systemDataSource struct {
	client *client
}

func newSystemDataSource() datasource.DataSource {
	return &systemDataSource{}
}

type systemModel struct {
	Model      types.String `tfsdk:"model"`
	Firmware   types.String `tfsdk:"firmware"`
	Hardware   types.String `tfsdk:"hardware"`
	MACAddress types.String `tfsdk:"mac_address"`
	IPAddress  types.String `tfsdk:"ip_address"`
}

func (d *systemDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system"
}

func (d *systemDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Switch identity and firmware.",
		Attributes: map[string]schema.Attribute{
			"model":       schema.StringAttribute{Computed: true},
			"firmware":    schema.StringAttribute{Computed: true},
			"hardware":    schema.StringAttribute{Computed: true},
			"mac_address": schema.StringAttribute{Computed: true},
			"ip_address":  schema.StringAttribute{Computed: true},
		},
	}
}

func (d *systemDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configureClient(req.ProviderData, &resp.Diagnostics)
}

func (d *systemDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	s, err := d.client.Status(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading switch status", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, systemModel{
		Model:      types.StringValue(s.Model),
		Firmware:   types.StringValue(s.Firmware),
		Hardware:   types.StringValue(s.Hardware),
		MACAddress: types.StringValue(s.MACAddress),
		IPAddress:  types.StringValue(s.IPAddress),
	})...)
}
