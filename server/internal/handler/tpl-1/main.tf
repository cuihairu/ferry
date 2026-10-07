terraform {
  required_providers {
    vultr = { source = "vultr/vultr" }
  }
}

variable "api_key" { type = string, sensitive = true }

provider "vultr" { api_key = var.api_key }

resource "vultr_instance" "node" {
  plan   = "vc2-1c-1gb"
  region = "hkg"
  label  = "hk-3t"
  
}
