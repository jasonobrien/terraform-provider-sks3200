resource "sks3200_vlan_8021q" "guest" {
  vlan_id        = 20
  untagged_ports = [3]
}

# Referencing the VLAN orders destroy: the PVID resets to 1 before VLAN 20
# goes away.
resource "sks3200_port_vlan" "p3" {
  port            = 3
  pvid            = sks3200_vlan_8021q.guest.vlan_id
  accepted_frames = "untagged"
}
