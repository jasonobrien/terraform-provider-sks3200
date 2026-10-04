data "sks3200_system" "this" {}

output "firmware" {
  value = data.sks3200_system.this.firmware
}
