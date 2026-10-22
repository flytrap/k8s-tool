#!/usr/bin/env bash
source "$(dirname "$0")/../common.sh"
require_root "$@"
platform
[[ $# == 6 ]] || fail 'Usage: install.sh VIP state localIP peerCSV priority region'
validate_ip "$1"
validate_ip "$3"
[[ $2 == MASTER || $2 == BACKUP ]] || fail 'Invalid VRRP state'
[[ $5 =~ ^[0-9]+$ ]] && ((10#$5 >= 1 && 10#$5 <= 254)) || fail 'Invalid VRRP priority'
[[ -z $6 || $6 =~ ^[a-z]{2}-[a-z]+-[0-9]+$ ]] || fail 'Invalid AWS region'
IFS=, read -r -a peers <<< "$4"
for peer in "${peers[@]}"; do [[ -z $peer ]] || validate_ip "$peer"; done
interface=$(ip -o -4 addr show | awk -v target="$3" '{split($4,a,"/"); if(a[1]==target){print $2; exit}}')
[[ $interface =~ ^[a-zA-Z0-9._:-]+$ ]] || fail 'Cannot find local VRRP interface'
apt-get update
apt_install keepalived
if [[ -n $6 ]]; then
  command -v aws >/dev/null || apt_install awscli
fi
workdir
cat > "$WORK/keepalived.conf" <<EOF
global_defs {
  router_id K8S_API
  script_user root
  enable_script_security
}
vrrp_script chk_haproxy {
  script "/usr/bin/systemctl is-active --quiet haproxy"
  interval 2
  fall 2
  rise 2
}
vrrp_instance VI_1 {
  state $2
  interface $interface
  virtual_router_id 51
  priority $5
  advert_int 1
EOF
if [[ -n $4 ]]; then
  printf '  unicast_src_ip %s\n' "$3" >> "$WORK/keepalived.conf"
fi
if [[ -n $4 ]]; then
  printf '  unicast_peer {\n' >> "$WORK/keepalived.conf"
  printf '    %s\n' "${peers[@]}" >> "$WORK/keepalived.conf"
  printf '  }\n' >> "$WORK/keepalived.conf"
fi
cat >> "$WORK/keepalived.conf" <<EOF
  virtual_ipaddress {
    $1/32
  }
  track_script {
    chk_haproxy
  }
EOF
if [[ -n $6 ]]; then
  install -m 0755 "$(dirname "$0")/aws-vip.sh" /etc/keepalived/aws-vip.sh
  printf '  notify_master "/etc/keepalived/aws-vip.sh assign %s %s"\n' "$1" "$6" >> "$WORK/keepalived.conf"
  # Do not unassign on BACKUP: a later callback could remove the new master's VIP.
fi
printf '}\n' >> "$WORK/keepalived.conf"
install -d -m 0755 /etc/keepalived
install -m 0644 "$WORK/keepalived.conf" /etc/keepalived/keepalived.conf.new
if keepalived --config-test -f /etc/keepalived/keepalived.conf.new; then
  :
else
  status=$?
  printf 'Keepalived configuration validation failed (exit %s). Candidate retained at /etc/keepalived/keepalived.conf.new\n' "$status" >&2
  sed -n '1,240p' /etc/keepalived/keepalived.conf.new >&2
  exit "$status"
fi
mv /etc/keepalived/keepalived.conf.new /etc/keepalived/keepalived.conf
systemctl enable keepalived
systemctl restart keepalived
