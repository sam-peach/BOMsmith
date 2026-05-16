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

variable "pricing_provider" {
  description = "Optional override for PRICING_PROVIDER. Empty / \"auto\" / \"multi\" composes every provider with credentials (fixed Mouser→Farnell→DigiKey→TME order). A single name (\"mouser\"|\"farnell\"|\"digikey\"|\"tme\") pins one source. \"mock\" is the incident kill switch; \"csv-only\" disables upstream pricing."
  type        = string
  default     = ""
}

variable "mouser_api_key" {
  description = "Mouser Search API key (free tier). Bare query-param auth. Empty disables the Mouser provider."
  type        = string
  default     = ""
  sensitive   = true
}

variable "farnell_api_key" {
  description = "Farnell / element14 Product Search API key (free tier). Empty disables the Farnell provider."
  type        = string
  default     = ""
  sensitive   = true
}

variable "farnell_store_id" {
  description = "Farnell store id that fixes the price currency (e.g. uk.farnell.com → GBP). Defaults to uk.farnell.com in code."
  type        = string
  default     = ""
}

variable "digikey_client_id" {
  description = "Digi-Key Product Information API OAuth2 client id (free tier). Paired with digikey_client_secret; both required to enable the Digi-Key provider."
  type        = string
  default     = ""
  sensitive   = true
}

variable "digikey_client_secret" {
  description = "Digi-Key OAuth2 client secret — paired with digikey_client_id"
  type        = string
  default     = ""
  sensitive   = true
}

variable "tme_token" {
  description = "TME API token (public id). Paired with tme_app_secret; both required to enable the TME provider."
  type        = string
  default     = ""
  sensitive   = true
}

variable "tme_app_secret" {
  description = "TME app secret — the HMAC-SHA1 signing key, paired with tme_token"
  type        = string
  default     = ""
  sensitive   = true
}
