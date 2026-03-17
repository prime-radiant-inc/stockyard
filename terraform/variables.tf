variable "instance_type" {
  default = "c8i.xlarge"
}

variable "ami_id" {
  description = "Packer-built AMI ID for stockyard hosts"
  type        = string
}

variable "instance_on" {
  description = "Set to false to terminate instances while keeping infra"
  type        = bool
  default     = true
}

variable "stockyard_version" {
  description = "Version of stockyard release to deploy (git SHA or 'latest')"
  type        = string
  default     = "latest"
}

variable "tailscale_auth_key" {
  type      = string
  sensitive = true
}

variable "anthropic_api_key" {
  type      = string
  sensitive = true
  default   = ""
}

variable "github_token" {
  type      = string
  sensitive = true
  default   = ""
}
