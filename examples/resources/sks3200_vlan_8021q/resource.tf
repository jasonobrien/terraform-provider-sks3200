resource "sks3200_vlan_8021q" "guest" {
  vlan_id        = 20
  name           = "guest"
  untagged_ports = [3, 4]
  tagged_ports   = [9]
}
