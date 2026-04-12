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
