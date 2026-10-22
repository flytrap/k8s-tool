#!/usr/bin/env bash
set -euo pipefail
[[ $# == 3 && $1 == assign ]] || exit 1
vip=$2
region=$3
metadata=http://169.254.169.254/latest
# IMDSv2 is the link-local metadata exception to HTTPS-only artifact downloads.
token=$(curl --fail --silent --show-error --connect-timeout 3 --max-time 10 -X PUT "$metadata/api/token" -H 'X-aws-ec2-metadata-token-ttl-seconds: 60')
instance=$(curl --fail --silent --show-error --connect-timeout 3 --max-time 10 -H "X-aws-ec2-metadata-token: $token" "$metadata/meta-data/instance-id")
eni=$(aws ec2 describe-instances --instance-ids "$instance" --region "$region" --query 'Reservations[0].Instances[0].NetworkInterfaces[?Attachment.DeviceIndex==`0`].NetworkInterfaceId | [0]' --output text)
[[ $eni == eni-* ]] || exit 1
aws ec2 assign-private-ip-addresses --network-interface-id "$eni" --private-ip-addresses "$vip" --allow-reassignment --region "$region"
