# =============================================================================
# Stockyard service deployment
# Uses the machine-service module from the terminus repo.
# =============================================================================

locals {
  infra = data.terraform_remote_state.infra.outputs
}

module "stockyard" {
  source = "git::ssh://git@github.com/prime-radiant-inc/terminus.git//terraform/modules/machine-service?ref=main"

  name          = "stockyard"
  instance_type = var.instance_type
  ami_id        = var.ami_id
  vpc_id        = local.infra.vpc_id
  subnet_ids    = local.infra.public_subnet_ids

  desired_capacity = var.instance_on ? 1 : 0
  max_instances    = 1
  use_spot         = true
  root_volume_size = 100

  cpu_options = {
    nested_virtualization = "enabled"
  }

  ssm_parameters = {
    TAILSCALE_AUTH_KEY = var.tailscale_auth_key
    ANTHROPIC_API_KEY  = var.anthropic_api_key
    GITHUB_TOKEN       = var.github_token
  }

  iam_policy_statements = [{
    Effect   = "Allow"
    Action   = ["s3:GetObject"]
    Resource = ["arn:aws:s3:::${local.infra.artifacts_bucket}/stockyard/*"]
  }]

  user_data = templatefile("${path.module}/user-data.sh.tftpl", {
    region            = local.infra.region
    artifacts_bucket  = local.infra.artifacts_bucket
    stockyard_version = var.stockyard_version
  })
}
