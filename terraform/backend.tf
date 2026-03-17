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
