# Smoke test against a real switch. Uses ports 1-3 and VLAN 999; keep the
# uplink off those ports. Run from this directory:
#
#   TF_CLI_CONFIG_FILE=$PWD/dev.tfrc terraform apply
#   TF_CLI_CONFIG_FILE=$PWD/dev.tfrc terraform destroy

terraform {
  required_providers {
    sks3200 = { source = "jasonobrien/sks3200" }
  }
}

variable "url" {
  default = "http://192.168.0.6"
}

provider "sks3200" {
  url = var.url
  # Password from $SKS3200_PASSWORD.
}

data "sks3200_system" "this" {}

output "system" {
  value = data.sks3200_system.this
}

resource "sks3200_vlan_8021q" "test" {
  vlan_id        = 999
  name           = "tftest"
  untagged_ports = [1]
  tagged_ports   = [2, 3]
}

# Referencing the VLAN orders destroy: the PVID resets to 1 before VLAN 999
# goes away.
resource "sks3200_port_vlan" "p1" {
  port            = 1
  pvid            = sks3200_vlan_8021q.test.vlan_id
  accepted_frames = "untagged"
}
