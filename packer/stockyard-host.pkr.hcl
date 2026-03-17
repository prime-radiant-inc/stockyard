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

  # No default VPC in this account — discover terminus VPC by tag
  vpc_filter {
    filters = {
      "tag:Name" = "terminus"
    }
  }

  subnet_filter {
    filters = {
      "tag:Name" = "terminus-public-*"
    }
    most_free = true
  }

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
      "sudo apt-get install -y build-essential git zfsutils-linux jq docker.io unzip",
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

  # systemd unit (from existing repo file)
  provisioner "file" {
    source      = "../scripts/stockyardd.service"
    destination = "/tmp/stockyardd.service"
  }

  provisioner "shell" {
    inline = [
      "sudo mv /tmp/stockyardd.service /etc/systemd/system/stockyardd.service",
      "sudo systemctl daemon-reload",
    ]
  }
}
