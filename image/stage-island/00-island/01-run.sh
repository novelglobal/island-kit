#!/bin/bash -e
install -m 755 files/island "${ROOTFS_DIR}/usr/local/bin/island"
install -m 644 files/island.service "${ROOTFS_DIR}/etc/systemd/system/island.service"
install -d "${ROOTFS_DIR}/etc/NetworkManager/dnsmasq-shared.d"
install -m 644 files/island-dns.conf "${ROOTFS_DIR}/etc/NetworkManager/dnsmasq-shared.d/island.conf"
CFG="${ROOTFS_DIR}/boot/firmware/config.txt"
[ -f "$CFG" ] || CFG="${ROOTFS_DIR}/boot/config.txt"
grep -q '^dtoverlay=gpio-key' "$CFG" || printf '
[all]
dtoverlay=gpio-key,gpio=3,keycode=148,label="island"
' >> "$CFG"

on_chroot << CHROOT
systemctl enable island.service
CHROOT
