package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/jasonobrien/terraform-provider-sks3200/internal/sks3200"
)

type sks3200Provider struct {
	version string
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &sks3200Provider{version: version}
	}
}

// client is shared with resources and data sources.
type client struct {
	*sks3200.Client
	url      string
	autosave bool
}

// commit saves the running config when autosave is on.
func (c *client) commit(ctx context.Context, diags *diag.Diagnostics) {
	if !c.autosave {
		return
	}
	if err := c.Save(ctx); err != nil {
		diags.AddError("Saving switch config", err.Error())
	}
}

type providerModel struct {
	URL      types.String `tfsdk:"url"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
	Autosave types.Bool   `tfsdk:"autosave"`
}

func (p *sks3200Provider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "sks3200"
	resp.Version = p.version
}

func (p *sks3200Provider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "XikeStor SKS3200-8E2X switches on firmware 2.0.x.",
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				Description: "Base URL of the switch web UI, e.g. http://192.168.10.12. Defaults to $SKS3200_URL.",
				Optional:    true,
			},
			"username": schema.StringAttribute{
				Description: `Defaults to $SKS3200_USERNAME, then "admin".`,
				Optional:    true,
			},
			"password": schema.StringAttribute{
				Description: "Defaults to $SKS3200_PASSWORD.",
				Optional:    true,
				Sensitive:   true,
			},
			"autosave": schema.BoolAttribute{
				Description: "Save the running config after each change; otherwise changes are lost on reboot. Defaults to true.",
				Optional:    true,
			},
		},
	}
}

func stringOr(v types.String, env, def string) string {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueString()
	}
	if s := os.Getenv(env); s != "" {
		return s
	}
	return def
}

func (p *sks3200Provider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	url := stringOr(cfg.URL, "SKS3200_URL", "")
	if url == "" {
		resp.Diagnostics.AddError("Missing url", "Set url in the provider block or $SKS3200_URL.")
		return
	}
	username := stringOr(cfg.Username, "SKS3200_USERNAME", "admin")
	password := stringOr(cfg.Password, "SKS3200_PASSWORD", "")

	c, err := sks3200.New(url, username, password)
	if err != nil {
		resp.Diagnostics.AddError("Creating client", err.Error())
		return
	}
	shared := &client{
		Client:   c,
		url:      url,
		autosave: cfg.Autosave.IsNull() || cfg.Autosave.ValueBool(),
	}
	resp.DataSourceData = shared
	resp.ResourceData = shared
}

func (p *sks3200Provider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newIPSettingsResource,
		newVLANResource,
		newPortVLANResource,
	}
}

func (p *sks3200Provider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newSystemDataSource,
	}
}

// configureClient extracts the shared client in resource/data source Configure.
func configureClient(data any, diags *diag.Diagnostics) *client {
	if data == nil {
		return nil // Provider not yet configured.
	}
	c, ok := data.(*client)
	if !ok {
		diags.AddError("Unexpected provider data", "Expected *client.")
		return nil
	}
	return c
}
