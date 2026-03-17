# Stockyard Deploy Pipeline Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Terraform, GitHub Actions CI, and Packer to the stockyard repo so it deploys onto Terminus infrastructure.

**Architecture:** CI builds Go binaries + VM image, uploads to S3. Packer bakes a host AMI with system deps. Terraform (using the `machine-service` module from terminus) creates an ASG that launches instances from that AMI. Instances download artifacts from S3 and secrets from SSM at boot.

**Tech Stack:** Terraform >= 1.5, Packer, GitHub Actions, AWS (S3, SSM, EC2/ASG, IAM OIDC), Go

**Spec:** `docs/specs/2026-03-16-stockyard-deploy-design.md`

---

## Chunk 1: OIDC IAM Role in Terminus

The GitHub Actions workflow needs AWS credentials via OIDC. The OIDC provider
already exists in the account (created by sen-deploy). The terminus infra root
needs a role for the stockyard repo's CI.

### Task 1: Add OIDC role to terminus infra root

**Files:**
- Create: `/Users/matt/Code/prime/terminus/terraform/infra/ci.tf`
- Modify: `/Users/matt/Code/prime/terminus/terraform/infra/outputs.tf`

- [ ] **Step 1: Reference the existing OIDC provider via data source**

The OIDC provider `https://token.actions.githubusercontent.com` already exists
(created by sen-deploy's `ci.tf`). Use a `data` source, not a new resource.

Create `ci.tf`:

```hcl
# =============================================================================
# CI/CD IAM Resources for GitHub Actions (OIDC)
# =============================================================================
# The OIDC provider itself is created by sen-deploy. We reference it here.

data "aws_iam_openid_connect_provider" "github" {
  url = "https://token.actions.githubusercontent.com"
}

# --- Stockyard CI Role ---

resource "aws_iam_role" "stockyard_ci" {
  name = "terminus-stockyard-ci"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Federated = data.aws_iam_openid_connect_provider.github.arn
      }
      Action = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
          "token.actions.githubusercontent.com:sub" = "repo:prime-radiant-inc/stockyard:ref:refs/heads/main"
        }
      }
    }]
  })

  tags = local.tags
}

resource "aws_iam_role_policy" "stockyard_ci" {
  name = "stockyard-ci-s3"
  role = aws_iam_role.stockyard_ci.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "s3:PutObject",
        "s3:GetObject"
      ]
      Resource = "${aws_s3_bucket.artifacts.arn}/stockyard/*"
    }]
  })
}
```

- [ ] **Step 2: Add the role ARN to outputs**

Append to `outputs.tf`:

```hcl
output "stockyard_ci_role_arn" {
  value = aws_iam_role.stockyard_ci.arn
}
```

- [ ] **Step 3: Apply**

```bash
cd /Users/matt/Code/prime/terminus/terraform/infra
terraform plan
terraform apply
```

Expected: 2-3 resources created (role + policy, possibly data source). Note the
role ARN from the output — it goes in the GitHub Actions workflow.

- [ ] **Step 4: Commit**

```bash
cd /Users/matt/Code/prime/terminus
git add terraform/infra/ci.tf terraform/infra/outputs.tf
git commit -m "feat: add OIDC CI role for stockyard GitHub Actions"
```

---

## Chunk 2: Stockyard Terraform

### Task 2: Update .gitignore

**Files:**
- Modify: `/Users/matt/Code/prime/stockyard/.gitignore`

- [ ] **Step 1: Add Terraform patterns to existing .gitignore**

Append to the end of `.gitignore`:

```
# Terraform
.terraform/
*.tfstate
*.tfstate.backup
*.tfvars
.terraform.lock.hcl
```

- [ ] **Step 2: Commit**

```bash
cd /Users/matt/Code/prime/stockyard
git add .gitignore
git commit -m "chore: add terraform patterns to gitignore"
```

### Task 3: Create Terraform backend and remote state

**Files:**
- Create: `/Users/matt/Code/prime/stockyard/terraform/backend.tf`

- [ ] **Step 1: Write backend.tf**

```hcl
terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }

  backend "s3" {
    bucket         = "terminus-terraform-state"
    key            = "services/stockyard/terraform.tfstate"
    region         = "us-west-1"
    dynamodb_table = "terminus-terraform-locks"
  }
}

provider "aws" {
  region = data.terraform_remote_state.infra.outputs.region
}

data "terraform_remote_state" "infra" {
  backend = "s3"
  config = {
    bucket = "terminus-terraform-state"
    key    = "infra/terraform.tfstate"
    region = "us-west-1"
  }
}
```

- [ ] **Step 2: Commit**

```bash
git add terraform/backend.tf
git commit -m "feat(terraform): add S3 backend and terminus remote state"
```

### Task 4: Create Terraform variables

**Files:**
- Create: `/Users/matt/Code/prime/stockyard/terraform/variables.tf`

- [ ] **Step 1: Write variables.tf**

```hcl
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
}

variable "github_token" {
  type      = string
  sensitive = true
}
```

- [ ] **Step 2: Commit**

```bash
git add terraform/variables.tf
git commit -m "feat(terraform): add variables"
```

### Task 5: Create Terraform main.tf

**Files:**
- Create: `/Users/matt/Code/prime/stockyard/terraform/main.tf`

- [ ] **Step 1: Write main.tf**

```hcl
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
```

- [ ] **Step 2: Commit**

```bash
git add terraform/main.tf
git commit -m "feat(terraform): add main.tf with machine-service module"
```

### Task 6: Create user-data template

**Files:**
- Create: `/Users/matt/Code/prime/stockyard/terraform/user-data.sh.tftpl`

- [ ] **Step 1: Write user-data.sh.tftpl**

```bash
#!/bin/bash
# Stockyard host bootstrap — thin version.
# System deps are pre-baked in the AMI (Packer). This script:
#   1. Connects to Tailscale
#   2. Downloads pre-built artifacts from S3
#   3. Sets up ZFS
#   4. Configures networking
#   5. Initializes stockyard
#   6. Starts the daemon

set -euo pipefail
exec > >(tee /var/log/stockyard-bootstrap.log) 2>&1
echo "=== Stockyard Bootstrap $(date) ==="

REGION="${region}"
ARTIFACTS_BUCKET="${artifacts_bucket}"
VERSION="${stockyard_version}"

ssm_get() {
  aws ssm get-parameter --region "$REGION" --name "$1" --with-decryption --query 'Parameter.Value' --output text
}

# ============================================
# 1. Tailscale
# ============================================
echo "[1/6] Connecting to Tailscale..."
TS_KEY=$(ssm_get /stockyard/TAILSCALE_AUTH_KEY)
tailscale up --authkey="$TS_KEY" --hostname="stockyard-$(hostname -s)" --ssh
echo "Tailscale connected"

# ============================================
# 2. Download release artifacts from S3
# ============================================
echo "[2/6] Downloading stockyard $VERSION..."
mkdir -p /var/lib/stockyard

if [ "$VERSION" = "latest" ]; then
  VERSION=$(aws s3 cp "s3://$ARTIFACTS_BUCKET/stockyard/latest" - 2>/dev/null || echo "")
  if [ -z "$VERSION" ]; then
    echo "ERROR: Could not resolve latest version from S3"
    exit 1
  fi
fi

aws s3 cp "s3://$ARTIFACTS_BUCKET/stockyard/$VERSION/stockyardd" /usr/local/bin/stockyardd
aws s3 cp "s3://$ARTIFACTS_BUCKET/stockyard/$VERSION/stockyard" /usr/local/bin/stockyard
aws s3 cp "s3://$ARTIFACTS_BUCKET/stockyard/$VERSION/rootfs.ext4" /var/lib/stockyard/rootfs.ext4
aws s3 cp "s3://$ARTIFACTS_BUCKET/stockyard/$VERSION/vmlinux.bin" /var/lib/stockyard/vmlinux.bin
chmod +x /usr/local/bin/stockyard /usr/local/bin/stockyardd

# ============================================
# 3. ZFS
# ============================================
echo "[3/6] Setting up ZFS..."
truncate -s 50G /var/lib/stockyard/zpool.img
zpool create tank /var/lib/stockyard/zpool.img
zfs create tank/stockyard
zfs create tank/stockyard/workspaces
zfs set compression=lz4 tank/stockyard

# ============================================
# 4. Networking (bridge + NAT)
# ============================================
echo "[4/6] Configuring networking..."
ip link add flbr0 type bridge
ip addr add 10.0.100.1/24 dev flbr0
ip link set flbr0 up

sysctl -w net.ipv4.ip_forward=1
echo "net.ipv4.ip_forward=1" > /etc/sysctl.d/99-stockyard.conf

iptables -t nat -A POSTROUTING -s 10.0.100.0/24 ! -o flbr0 -j MASQUERADE
iptables -A FORWARD -i flbr0 -j ACCEPT
iptables -A FORWARD -o flbr0 -j ACCEPT

# ============================================
# 5. Initialize stockyard
# ============================================
echo "[5/6] Initializing stockyard..."
stockyard init --instance "stockyard-$(hostname -s)"
jq '.secrets.provider = "file" | .secrets.dir = "/etc/stockyard/secrets"' \
  /etc/stockyard/config.json > /tmp/stockyard-config.json \
  && mv /tmp/stockyard-config.json /etc/stockyard/config.json

mkdir -p /etc/stockyard/secrets
chmod 700 /etc/stockyard/secrets
ssm_get /stockyard/ANTHROPIC_API_KEY > /etc/stockyard/secrets/anthropic-api-key
ssm_get /stockyard/GITHUB_TOKEN > /etc/stockyard/secrets/github-token
echo -n "$TS_KEY" > /etc/stockyard/secrets/tailscale-auth-key
chmod 600 /etc/stockyard/secrets/*

# ============================================
# 6. Start daemon
# ============================================
echo "[6/6] Starting stockyardd..."
systemctl daemon-reload
systemctl enable stockyardd
systemctl start stockyardd

echo ""
echo "=== Stockyard Ready $(date) ==="
```

- [ ] **Step 2: Commit**

```bash
git add terraform/user-data.sh.tftpl
git commit -m "feat(terraform): add thin user-data boot script"
```

### Task 7: Create Terraform outputs

**Files:**
- Create: `/Users/matt/Code/prime/stockyard/terraform/outputs.tf`

- [ ] **Step 1: Write outputs.tf**

```hcl
output "asg_name" {
  value = module.stockyard.asg_name
}

output "security_group_id" {
  value = module.stockyard.security_group_id
}
```

- [ ] **Step 2: Commit**

```bash
git add terraform/outputs.tf
git commit -m "feat(terraform): add outputs"
```

---

## Chunk 3: Packer

### Task 8: Create Packer template

**Files:**
- Create: `/Users/matt/Code/prime/stockyard/packer/stockyard-host.pkr.hcl`

- [ ] **Step 1: Write the Packer template**

```hcl
packer {
  required_plugins {
    amazon = {
      version = ">= 1.2.0"
      source  = "github.com/hashicorp/amazon"
    }
  }
}

variable "region" {
  default = "us-west-1"
}

variable "firecracker_version" {
  default = "v1.10.1"
}

source "amazon-ebs" "stockyard" {
  ami_name      = "stockyard-host-{{timestamp}}"
  instance_type = "c8i.xlarge"
  region        = var.region

  source_ami_filter {
    filters = {
      name                = "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"
      virtualization-type = "hvm"
    }
    most_recent = true
    owners      = ["099720109477"]
  }

  ssh_username = "ubuntu"

  tags = {
    Name       = "stockyard-host"
    managed_by = "packer"
  }
}

build {
  sources = ["source.amazon-ebs.stockyard"]

  # System packages
  provisioner "shell" {
    inline = [
      "sudo apt-get update",
      "sudo apt-get install -y build-essential git zfsutils-linux jq docker.io",
    ]
  }

  # Firecracker
  provisioner "shell" {
    inline = [
      "cd /tmp",
      "ARCH=$(uname -m)",
      "curl -LO https://github.com/firecracker-microvm/firecracker/releases/download/${var.firecracker_version}/firecracker-${var.firecracker_version}-$${ARCH}.tgz",
      "tar -xzf firecracker-${var.firecracker_version}-$${ARCH}.tgz",
      "sudo mv release-${var.firecracker_version}-$${ARCH}/firecracker-${var.firecracker_version}-$${ARCH} /usr/local/bin/firecracker",
      "sudo mv release-${var.firecracker_version}-$${ARCH}/jailer-${var.firecracker_version}-$${ARCH} /usr/local/bin/jailer",
      "sudo chmod +x /usr/local/bin/firecracker /usr/local/bin/jailer",
      "rm -rf release-${var.firecracker_version}-$${ARCH} firecracker-${var.firecracker_version}-$${ARCH}.tgz",
    ]
  }

  # Tailscale
  provisioner "shell" {
    inline = [
      "curl -fsSL https://tailscale.com/install.sh | sudo sh",
    ]
  }

  # AWS CLI
  provisioner "shell" {
    inline = [
      "curl -fsSL https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip -o /tmp/awscliv2.zip",
      "cd /tmp && unzip -q awscliv2.zip && sudo ./aws/install && rm -rf aws awscliv2.zip",
    ]
  }

  # Stockyard directories
  provisioner "shell" {
    inline = [
      "sudo mkdir -p /var/lib/stockyard /etc/stockyard /var/log/stockyard",
    ]
  }

  # systemd unit for stockyardd
  provisioner "shell" {
    inline = [
      "cat <<'UNIT' | sudo tee /etc/systemd/system/stockyardd.service",
      "[Unit]",
      "Description=Stockyard Daemon",
      "After=network-online.target",
      "Wants=network-online.target",
      "",
      "[Service]",
      "Type=simple",
      "ExecStart=/usr/local/bin/stockyardd",
      "Restart=on-failure",
      "RestartSec=5",
      "",
      "[Install]",
      "WantedBy=multi-user.target",
      "UNIT",
      "sudo systemctl daemon-reload",
    ]
  }
}
```

Note: The systemd unit is inlined here. If stockyard already has a
`scripts/stockyardd.service` file, use a `file` provisioner to copy it instead.
Check `scripts/stockyardd.service` in the stockyard repo before implementing.

- [ ] **Step 2: Commit**

```bash
git add packer/stockyard-host.pkr.hcl
git commit -m "feat(packer): add stockyard host AMI template"
```

### Task 9: Build the AMI (manual)

- [ ] **Step 1: Init and build**

```bash
cd /Users/matt/Code/prime/stockyard/packer
packer init .
packer build .
```

Expected: Packer launches a temporary EC2 instance, provisions it, creates an
AMI, and prints the AMI ID. Takes ~5-10 minutes.

- [ ] **Step 2: Note the AMI ID**

Save the AMI ID (e.g. `ami-0abc123def456`) — it goes into the Terraform
variables.

---

## Chunk 4: GitHub Actions CI

### Task 10: Create build workflow

**Files:**
- Create: `/Users/matt/Code/prime/stockyard/.github/workflows/build.yml`

The role ARN comes from the terminus `terraform output stockyard_ci_role_arn`.

- [ ] **Step 1: Write build.yml**

```yaml
name: Build and Upload

on:
  push:
    branches: [main]

permissions:
  id-token: write
  contents: read

env:
  AWS_REGION: us-west-1
  ARTIFACTS_BUCKET: terminus-deploy-artifacts

jobs:
  build:
    name: Build and Upload
    runs-on: ubuntu-latest
    timeout-minutes: 30

    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Configure AWS credentials (OIDC)
        uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: ${{ vars.AWS_ROLE_ARN }}
          aws-region: ${{ env.AWS_REGION }}

      - name: Build binaries
        run: make build

      - name: Build stockyard-shell
        run: make build-shell

      - name: Build VM image
        run: |
          cd vm-image
          ./build.sh
          sudo ./convert-to-rootfs.sh

      - name: Upload artifacts to S3
        run: |
          SHA="${{ github.sha }}"
          PREFIX="s3://${{ env.ARTIFACTS_BUCKET }}/stockyard/${SHA}"

          aws s3 cp bin/stockyardd "${PREFIX}/stockyardd"
          aws s3 cp bin/stockyard "${PREFIX}/stockyard"
          aws s3 cp vm-image/output/rootfs.ext4 "${PREFIX}/rootfs.ext4"
          aws s3 cp vm-image/output/vmlinux.bin "${PREFIX}/vmlinux.bin"

          # Update latest pointer
          echo -n "${SHA}" | aws s3 cp - "s3://${{ env.ARTIFACTS_BUCKET }}/stockyard/latest"

          echo "### Artifacts uploaded" >> $GITHUB_STEP_SUMMARY
          echo "Version: \`${SHA}\`" >> $GITHUB_STEP_SUMMARY
          echo "S3: \`${PREFIX}/\`" >> $GITHUB_STEP_SUMMARY
```

Note: This builds the full VM image including the kernel on every push. The
first build will take ~20 minutes due to kernel compilation. Kernel caching
(skip compile when `kernel.config` hasn't changed) is a future optimization —
see the spec for the design.

- [ ] **Step 2: Set the AWS_ROLE_ARN repo variable on GitHub**

```bash
gh variable set AWS_ROLE_ARN \
  --repo prime-radiant-inc/stockyard \
  --body "$(cd /Users/matt/Code/prime/terminus/terraform/infra && terraform output -raw stockyard_ci_role_arn)"
```

- [ ] **Step 3: Commit**

```bash
cd /Users/matt/Code/prime/stockyard
git add .github/workflows/build.yml
git commit -m "feat(ci): add GitHub Actions build and upload workflow"
```

---

## Chunk 5: First Deploy

### Task 11: Initialize Terraform and deploy

- [ ] **Step 1: Create secrets.tfvars**

Create `/Users/matt/Code/prime/stockyard/terraform/secrets.tfvars` (this file
is gitignored):

```hcl
tailscale_auth_key = "tskey-auth-..."
anthropic_api_key  = "sk-ant-..."
github_token       = "gho_..."
```

Use the real secret values. This file never gets committed.

- [ ] **Step 2: Initialize Terraform**

```bash
cd /Users/matt/Code/prime/stockyard/terraform
terraform init
```

Expected: "Terraform has been successfully initialized." The module is pulled
from the terminus repo via git.

- [ ] **Step 3: Plan**

```bash
terraform plan \
  -var ami_id=<AMI_ID_FROM_PACKER> \
  -var-file=secrets.tfvars
```

Expected: ~10 resources to create (SG, IAM role, instance profile, launch
template, ASG, SSM parameters). Review the plan.

- [ ] **Step 4: Apply**

```bash
terraform apply \
  -var ami_id=<AMI_ID_FROM_PACKER> \
  -var-file=secrets.tfvars
```

- [ ] **Step 5: Verify instance boots**

The ASG will launch an instance. Check:

```bash
# Instance should appear in ASG
aws autoscaling describe-auto-scaling-groups \
  --region us-west-1 \
  --query 'AutoScalingGroups[?contains(AutoScalingGroupName, `stockyard`)].{Name:AutoScalingGroupName,Desired:DesiredCapacity,Running:Instances[?LifecycleState==`InService`] | length(@)}' \
  --output table
```

If `stockyard_version=latest` and no CI has run yet, the instance will fail to
boot (nothing in S3). Either:
- Push to main first to trigger CI, wait for artifacts to upload, then apply
- Or upload artifacts manually for the first deploy:

```bash
SHA=$(git rev-parse HEAD)
cd /Users/matt/Code/prime/stockyard
make build && make build-shell && make -C vm-image rootfs
aws s3 cp bin/stockyardd "s3://terminus-deploy-artifacts/stockyard/${SHA}/stockyardd"
aws s3 cp bin/stockyard "s3://terminus-deploy-artifacts/stockyard/${SHA}/stockyard"
aws s3 cp vm-image/output/rootfs.ext4 "s3://terminus-deploy-artifacts/stockyard/${SHA}/rootfs.ext4"
aws s3 cp vm-image/output/vmlinux.bin "s3://terminus-deploy-artifacts/stockyard/${SHA}/vmlinux.bin"
echo -n "${SHA}" | aws s3 cp - "s3://terminus-deploy-artifacts/stockyard/latest"
```

Then apply with `stockyard_version=latest` or the specific SHA.

- [ ] **Step 6: Verify via Tailscale**

Once the instance boots, it should appear on your Tailscale network. SSH in:

```bash
ssh stockyard-<instance-hostname>
```

Check the bootstrap log:

```bash
cat /var/log/stockyard-bootstrap.log
systemctl status stockyardd
```

### Task 12: Push and verify CI

- [ ] **Step 1: Push all commits to main**

```bash
cd /Users/matt/Code/prime/stockyard
git push origin main
```

- [ ] **Step 2: Watch the CI run**

```bash
gh run watch --repo prime-radiant-inc/stockyard
```

Expected: Build completes, artifacts uploaded to S3 under the commit SHA.

- [ ] **Step 3: Verify artifacts in S3**

```bash
SHA=$(git rev-parse HEAD)
aws s3 ls "s3://terminus-deploy-artifacts/stockyard/${SHA}/"
```

Expected: `stockyardd`, `stockyard`, `rootfs.ext4`, `vmlinux.bin` all listed.

## Future Optimizations (not in this plan)

- **Kernel caching:** Cache `vmlinux.bin` in S3, skip kernel compile when
  `vm-image/kernel.config` hasn't changed. See spec for design.
- **Auto-deploy:** CI terminates instance after upload, ASG replaces it
  automatically. No manual `terraform apply` needed for code deploys.
- **Graceful drain:** Instance stops accepting new VMs, waits for existing
  ones to finish, then self-terminates. ASG lifecycle hook.
