#
# Minimal two-WAN failover daemon for OpenWrt.
#
# SPDX-License-Identifier: MIT
#

include $(TOPDIR)/rules.mk

PKG_NAME:=simplewan
PKG_VERSION:=0.2.0
PKG_RELEASE:=1

PKG_SOURCE_PROTO:=git
PKG_SOURCE_URL:=https://github.com/filippo-claude/simplewan.git
# CI overrides PKG_SOURCE_VERSION with the built commit/tag.
PKG_SOURCE_VERSION:=v$(PKG_VERSION)
PKG_MIRROR_HASH:=skip

PKG_MAINTAINER:=Filippo's Claude Agent <claude@filippo.io>
PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=LICENSE

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_BUILD_FLAGS:=no-mips16

GO_PKG:=github.com/filippo-claude/simplewan
GO_PKG_BUILD_PKG:=github.com/filippo-claude/simplewan/cmd/simplewand
GO_PKG_LDFLAGS_X:=main.version=$(PKG_VERSION)-r$(PKG_RELEASE)

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

define Package/simplewan
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=Routing and Redirection
  TITLE:=Minimal two-WAN failover daemon
  URL:=https://github.com/filippo-claude/simplewan
  DEPENDS:=$(GO_ARCH_DEPENDS) +ca-bundle +kmod-nf-conntrack-netlink
endef

define Package/simplewan/description
  A small failover daemon: it pings a target out of each WAN (bound to the
  WAN's device), tracks health with hysteresis and a recovery hold, and
  reorders the IPv4 default routes so the preferred healthy WAN wins. It never
  blackholes traffic and never tears routing down. Optional Postmark email on
  failover/recovery.
endef

define Package/simplewan/conffiles
/etc/config/simplewan
endef

define Package/simplewan/install
	$(call GoPackage/Package/Install/Bin,$(1))

	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/etc/config/simplewan $(1)/etc/config/simplewan

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/etc/init.d/simplewan $(1)/etc/init.d/simplewan
endef

$(eval $(call GoPackage/Default))
$(eval $(call BuildPackage,simplewan))
