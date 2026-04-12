variable "region" {
  description = "AWS region"
  type        = string
  default     = "eu-west-2"
}

variable "app_name" {
  description = "Name used for all resources"
  type        = string
  default     = "sme-prototype"
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
  default     = "praxis"
}

variable "db_password" {
  description = "Postgres master password for the RDS instance"
  type        = string
  sensitive   = true
}
