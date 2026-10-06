# COPR spec for ggcode - downloads the prebuilt release binary
# (simple and reliable; no golang toolchain needed in the buildroot).
# Update Version/Release and the two sha256 lines per release
# (same values as packaging/aur/PKGBUILD).

%global github https://github.com/topcheer/ggcode

Name:           ggcode
Version:        1.3.247
Release:        1%{?dist}
Summary:        AI coding agent for the terminal with a polished TUI
License:        MIT
URL:            %{github}
Source0:        %{github}/releases/download/v%{version}/ggcode_linux_x86_64.tar.gz
Source1:        %{github}/releases/download/v%{version}/ggcode_linux_arm64.tar.gz
# sha256 placeholders - refresh per release
# Source0: PLACEHOLDER_X86_64_SHA256
# Source1: PLACEHOLDER_AARCH64_SHA256
BuildArch:      noarch
Requires:       glibc
Provides:       ggcode

%description
AI coding agent for the terminal with a polished TUI, resumable
sessions, MCP integrations, built-in tools, and self-update support.

%prep

%build

%install
mkdir -p %{buildroot}%{_bindir}
case "$(uname -m)" in
    x86_64)  TAR=%{SOURCE0} ;;
    aarch64) TAR=%{SOURCE1} ;;
    *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac
tar -xzf "${TAR}" -C %{buildroot}%{_bindir} ggcode
chmod 755 %{buildroot}%{_bindir}/ggcode

%files
%license LICENSE
%{_bindir}/ggcode

%changelog
* Sat Sep 27 2026 GG AI Studio <topcheer@me.com> - 1.3.247-1
- Initial COPR packaging from release binary
