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

# ── Inbound email forwarding (ImprovMX) ────────────────────────────────────────
# Free email forwarding so we have a real business-domain mailbox
# (e.g. api@bomsmith.com → a personal inbox) without standing up a full
# mail server. Useful for vendor/API signups that reject free-mail
# (gmail/yahoo) addresses.
#
# DNS is only half the setup. The other half is in the ImprovMX dashboard
# (https://improvmx.com, free): add the domain `bomsmith.com` and create
# the alias api@bomsmith.com → <your inbox>. ImprovMX verifies ownership
# via these records, so create the alias first, then apply this.
#
# mx1/mx2 hostnames and the SPF include are ImprovMX's stable, documented
# values. The zone had zero MX/TXT records beforehand, so nothing is
# being displaced.

resource "aws_route53_record" "improvmx_mx" {
  zone_id = data.aws_route53_zone.main.zone_id
  name    = var.domain_name
  type    = "MX"
  ttl     = 300
  records = [
    "10 mx1.improvmx.com",
    "20 mx2.improvmx.com",
  ]
}

resource "aws_route53_record" "improvmx_spf" {
  zone_id = data.aws_route53_zone.main.zone_id
  name    = var.domain_name
  type    = "TXT"
  ttl     = 300
  records = ["v=spf1 include:spf.improvmx.com ~all"]
}
