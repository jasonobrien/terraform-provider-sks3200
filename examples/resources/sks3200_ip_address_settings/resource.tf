resource "sks3200_ip_address_settings" "this" {
  ip_address = "192.168.10.12"
  netmask    = "255.255.255.0"
  gateway    = "192.168.10.1"
}
