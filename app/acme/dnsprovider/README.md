# DNS Providers for ACME challenge
Following section describes how to configure DNS providers for ACME challenge. Reproxy supports configuration via configuration file and environment variables.

## Supported DNS providers:

### [Amazon Route 53](https://aws.amazon.com/route53/)
- **Access Key ID**                         yaml: `access_key_id`, env: `ROUTE53_ACCESS_KEY_ID`
- **Secret Access Key**                     yaml: `secret_access_key`, env: `ROUTE53_SECRET_ACCESS_KEY`
- **Hosted Zone ID**                        yaml: `hosted_zone_id`, env: `ROUTE53_HOSTED_ZONE_ID`
- TTL (optional, default `300s`)            yaml: `ttl`, env: `ROUTE53_TTL`
- Region(optional, default `us-east-1`)     yaml: `region`, env: `ROUTE53_REGION`

### [CloudDNS](https://www.cloudns.net/)
- **Authorized User ID**                    yaml:`auth_id` env:`CLOUDNS_AUTH_ID``
- **Authorized Subuser ID**                 yaml:`sub_auth_id` env:"`CLOUDNS_SUB_AUTH_ID` 
- **Password**                              yaml:`password` env:`CLOUDNS_AUTH_PASSWORD`
- TTL (optional, default `300s`)            yaml: `ttl` env:`CLOUDNS_TTL` 