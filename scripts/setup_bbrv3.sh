#!/usr/bin/env bash
# Provision Google's BBRv3 so the TCP arm can run it. BBRv3 is NOT a loadable
# module: it patches core TCP (a skb_marked_lost callback, TCP_CONG_WANTS_CE_EVENTS
# signaling, a changed tso_segs interface), so building google/bbr's tcp_bbr.c
# against a mainline kernel fails with "struct tcp_congestion_ops has no member
# named tso_segs / skb_marked_lost". You therefore need a custom kernel.
#
# This script uses the XanMod kernel, which packages the BBRv3 patches as an
# installable .deb. If XanMod's repository is unreachable (some hosts 403 it
# behind a bot wall), the fallback is to build the kernel from the google/bbr v3
# tree directly (github.com/google/bbr) using /boot/config-$(uname -r) as the
# base config, then pin it in GRUB and reboot.
#
# After a BBRv3 kernel is running, re-run the matrix. The confirmation that bbr
# is really v3 is behavioral: BBRv3 reintroduces a 2% loss ceiling
# (BBR.LossThresh), so its goodput on the loss sweep collapses around 2% where
# BBRv1 held to 5%.
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
	echo "error: run as root" >&2
	exit 1
fi

echo "current kernel: $(uname -r)"
echo "current bbr: mainline BBRv1 (baseline to compare against)"

apt-get update
apt-get install -y wget gnupg

# XanMod signing key and repository.
wget -qO - https://dl.xanmod.org/archive.key | gpg --dearmor -o /usr/share/keyrings/xanmod-archive-keyring.gpg
echo 'deb [signed-by=/usr/share/keyrings/xanmod-archive-keyring.gpg] http://deb.xanmod.org releases main' \
	> /etc/apt/sources.list.d/xanmod-release.list
apt-get update

# x64v3 targets the x86-64-v3 microarchitecture (AVX2); EPYC-Milan qualifies.
# Fall back to the generic package if that build is unavailable.
apt-get install -y linux-xanmod-x64v3 || apt-get install -y linux-xanmod || apt-get install -y linux-xanmod-lts

update-grub || true

echo
echo "XanMod installed. The mainline kernel remains in the GRUB menu as a fallback,"
echo "so a failed boot is recoverable from the Hetzner console."
echo "Next: reboot, then verify:"
echo "  uname -r                                             # expect *-xanmod*"
echo "  sysctl -n net.ipv4.tcp_available_congestion_control  # expect bbr present"
echo "Then re-run the loss sweep; bbr should now collapse near 2% loss (BBRv3)."
