variable "region" {
  description = "AWS region"
  type        = string
  default     = "eu-west-2"
}

variable "app_name" {
  description = "Name used for all resources"
  type        = string
  default     = "bomsmith"
}

variable "anthropic_api_key" {
  description = "Anthropic API key passed to the container as an environment variable"
  type        = string
  sensitive   = true
}

variable "auth_username" {
  description = "Initial admin username (used to seed the database on first boot)"
  type        = string
}

variable "auth_password" {
  description = "Initial admin password (used to seed the database on first boot)"
  type        = string
  sensitive   = true
}

variable "org_name" {
  description = "Name of the initial organisation created during database seed"
  type        = string
  default     = "Default Org"
}

variable "db_username" {
  description = "Postgres master username for the RDS instance"
  type        = string
  default     = "bomsmith"
}

variable "db_password" {
  description = "Postgres master password for the RDS instance"
  type        = string
  sensitive   = true
}

variable "domain_name" {
  description = "Registered domain name (e.g. bomsmith.io) — www subdomain will be the canonical URL"
  type        = string
}

variable "github_repo" {
  description = "GitHub repository in owner/repo format — scopes the OIDC deploy role to this repo's main branch"
  type        = string
  default     = "sam-peach/BOMsmith"
}

variable "nexar_client_id" {
  description = "Nexar / Octopart OAuth client id — backend auto-flips PRICING_PROVIDER to nexar when both this and nexar_client_secret are set. Leave empty to keep the mock provider in prod."
  type        = string
  default     = ""
  sensitive   = true
}

variable "nexar_client_secret" {
  description = "Nexar / Octopart OAuth client secret — paired with nexar_client_id"
  type        = string
  default     = ""
  sensitive   = true
}

variable "pricing_provider" {
  description = "Optional override for PRICING_PROVIDER. Leave empty to let the backend auto-detect (\"nexar\" when credentials present, else \"mock\"). Set to \"mock\" as a kill switch for incidents."
  type        = string
  default     = ""
}
