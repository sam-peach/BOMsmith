# ── Route 53 hosted zone ───────────────────────────────────────────────────────
# Assumes the domain is already registered and the hosted zone exists in this
# AWS account (Route 53 creates it automatically when you register a domain).

data "aws_route53_zone" "main" {
  name = var.domain_name
}

# ── App Runner custom domain ───────────────────────────────────────────────────
# App Runner issues and manages the TLS certificate itself — no ACM resource needed.
#
# TWO-PHASE APPLY REQUIRED:
#   Phase 1:  terraform apply -target=aws_apprunner_custom_domain_association.app
#   Phase 2:  terraform apply
#
# Phase 1 creates the association and persists certificate_validation_records in state.
# Phase 2 can then read those records and create the Route 53 entries.

resource "aws_apprunner_custom_domain_association" "app" {
  domain_name          = "www.${var.domain_name}"
  service_arn          = aws_apprunner_service.app.arn
  enable_www_subdomain = false
}

# Validation CNAMEs that App Runner needs to prove domain ownership and
# issue its TLS certificate. Only available after Phase 1.
resource "aws_route53_record" "apprunner_validation" {
  for_each = {
    for r in aws_apprunner_custom_domain_association.app.certificate_validation_records :
    r.name => r
  }
  zone_id         = data.aws_route53_zone.main.zone_id
  name            = each.value.name
  type            = each.value.type
  ttl             = 60
  records         = [each.value.value]
  allow_overwrite = true
}

# www.domain → App Runner service
resource "aws_route53_record" "www" {
  zone_id = data.aws_route53_zone.main.zone_id
  name    = "www.${var.domain_name}"
  type    = "CNAME"
  ttl     = 300
  records = [aws_apprunner_custom_domain_association.app.dns_target]
}
