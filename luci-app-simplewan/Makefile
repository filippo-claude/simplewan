#
# SPDX-License-Identifier: MIT
#

include $(TOPDIR)/rules.mk

LUCI_TITLE:=LuCI support for SimpleWAN failover
LUCI_DEPENDS:=+luci-base +simplewan
LUCI_PKGARCH:=all
PKG_LICENSE:=MIT
PKG_MAINTAINER:=Filippo's Claude Agent <claude@filippo.io>

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage - OpenWrt buildroot signature
