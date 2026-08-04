# Copyright 2026 Gentoo Authors
# Distributed under the terms of the GNU General Public License v2

EAPI=8

inherit shell-completion

DESCRIPTION="Prebuilt static Arise package manager for Gentoo"
HOMEPAGE="https://github.com/airencracken/arise"
SRC_URI="amd64? ( https://github.com/airencracken/arise/releases/download/v${PV}/${P}-linux-amd64.tar.xz )"
S="${WORKDIR}/${P}-linux-amd64"

LICENSE="GPL-3 Apache-2.0 BSD BSD-2 MIT MPL-2.0"
SLOT="0"
KEYWORDS="~amd64"

RDEPEND="
	!sys-apps/arise
	app-arch/bzip2
	app-arch/tar
	app-arch/xz-utils
	app-shells/bash
	sys-apps/portage
	sys-apps/sandbox
	sys-apps/util-linux
"
BDEPEND="sys-apps/file"

RESTRICT="strip"
QA_PREBUILT="usr/bin/arise"

src_prepare() {
	default
	local linkage
	local reported_version
	[[ -x arise ]] || die "release bundle is missing executable arise"
	[[ -f arise.1 ]] || die "release bundle is missing arise.1"
	[[ -f arise-completion.bash ]] || die "release bundle is missing Bash completion"
	[[ -f arise-artifact-manifest.json ]] || die "release bundle is missing its provenance manifest"
	reported_version=$(./arise --version) || die "could not execute release binary"
	[[ ${reported_version} == "arise ${PV}" ]] || die "release binary version does not match ${PV}"
	linkage=$(file arise) || die "could not inspect release binary"
	[[ ${linkage} == *"ELF 64-bit LSB executable"* && ${linkage} == *"x86-64"* && ${linkage} == *"statically linked"* ]] ||
		die "release binary is not a static amd64 ELF: ${linkage}"
}

src_install() {
	dobin arise
	doman arise.1
	newbashcomp arise-completion.bash arise
	dodoc README.md LICENSE arise-artifact-manifest.json
}

pkg_postinst() {
	elog "Installed the official prebuilt static Arise ${PV} binary."
	elog "Keep Portage installed as the reference and recovery implementation."
}
