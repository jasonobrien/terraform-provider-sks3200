# terraform-provider-sks3200

Terraform provider for XikeStor SKS3200-8E2X switches on firmware 2.0.x
(tested: 2.0.0.3). Older XikeStor/HRUI firmware (1.x, `*.cgi` pages) is
covered by [brennoo/hrui](https://registry.terraform.io/providers/brennoo/hrui)
instead; 2.0 replaced that UI with a JSON API, which this provider uses. The
API is undocumented; see [sks32kmon's API notes][api] and the switch's own
`*.html` pages.

[api]: https://github.com/planetf1/sks32kmon/blob/main/docs/API.md

## Resources

| Resource                      | Endpoint                          |
| ----------------------------- | --------------------------------- |
| `sks3200_ip_address_settings` | `network_settings{,_ipv4}.json`   |
| `sks3200_vlan_8021q`          | `tag_vlan.json` (802.1Q table)    |
| `sks3200_port_vlan`           | `port_vlan.json` (PVID, ingress)  |
| `sks3200_system` (data)       | `status.json`                     |

Changes are saved to flash after each write unless `autosave = false`.

```hcl
provider "sks3200" {
  url      = "http://192.168.0.6"
  password = var.switch_password
}

resource "sks3200_vlan_8021q" "guest" {
  vlan_id      = 20
  name         = "guest"
  tagged_ports = [9, 2]
}
```

VLAN 1 exists from the factory: import it (`terraform import
sks3200_vlan_8021q.default 1`) rather than creating it.

## Switch quirks

- Factory address is 192.168.10.12/24, login admin/admin.
- A port is untagged in at most one VLAN. Adding it as untagged elsewhere
  silently removes it from the old VLAN, so a Terraform-managed VLAN 1 that
  lists it will show drift.
- The switch won't drop a port from the VLAN that is its PVID. Move the PVID
  first, e.g. by making `sks3200_port_vlan.pvid` reference the VLAN resource.
- Changing `ip_address` moves the switch away from the provider `url`; the
  change is applied but not saved until the next apply at the new address.
- Login failures and expired sessions return HTTP 200 with a redirect to
  `login.html`; the client re-logs in once.

## Install

Not published; `terraform init` picks it up from Terraform's implicit local
mirror. Bump `V` for each change, or `init` fails on the lock file checksum.

```sh
V=0.1.0
D=~/.local/share/terraform/plugins/registry.terraform.io/jasonobrien/sks3200/$V/linux_amd64
mkdir -p $D && go build -ldflags "-X main.version=$V" -o $D/terraform-provider-sks3200_v$V .
```

## Development

```sh
go test ./...
go build -o terraform-provider-sks3200 .
cd examples/test && SKS3200_PASSWORD=admin TF_CLI_CONFIG_FILE=$PWD/dev.tfrc terraform apply
```
