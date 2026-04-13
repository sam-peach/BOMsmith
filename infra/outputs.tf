output "region" {
  description = "AWS region"
  value       = var.region
}

output "ecr_url" {
  description = "ECR repository URL (used when pushing images)"
  value       = aws_ecr_repository.app.repository_url
}

output "app_url" {
  description = "Public HTTPS URL for the deployed app"
  value       = "https://${aws_apprunner_service.app.service_url}"
}

output "rds_endpoint" {
  description = "RDS Postgres endpoint (host:port)"
  value       = "${aws_db_instance.postgres.address}:${aws_db_instance.postgres.port}"
}

output "github_deploy_role_arn" {
  description = "Set this as AWS_ROLE_ARN in GitHub repository secrets"
  value       = aws_iam_role.github_deploy.arn
}

output "custom_domain_url" {
  description = "Custom domain URL (live once DNS propagates and App Runner validates the certificate)"
  value       = "https://www.${var.domain_name}"
}

output "app_runner_service_arn" {
  description = "Set this as APP_RUNNER_SERVICE_ARN in GitHub repository secrets"
  value       = aws_apprunner_service.app.arn
}
