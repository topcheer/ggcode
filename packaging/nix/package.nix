# Nix packaging skeleton for ggcode (buildGoModule).
#
# vendorHash is a PLACEHOLDER - to compute it for a release:
#   1. git checkout v<X.Y.Z> && go mod vendor
#   2. nix hash path ./vendor     # newer nix, outputs sri form directly
#      (or: nix-hash --flat --base32 --type sha256 ./vendor for legacy form)
#   3. paste the output below (sri form "sha256-AAA..." preferred)
# Then: nix-build -A ggcode or add to your flake/overlays.
{
  lib,
  buildGoModule,
  fetchFromGitHub,
}:

buildGoModule rec {
  pname = "ggcode";
  version = "1.3.247"; # bump per release together with vendorHash

  src = fetchFromGitHub {
    owner = "topcheer";
    repo = "ggcode";
    rev = "v${version}";
    hash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="; # PLACEHOLDER: source tarball hash (nix hash from release tarball)
  };

  # PLACEHOLDER: compute via `go mod vendor` + `nix hash path ./vendor`
  # after bumping version. See header comment.
  vendorHash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";

  # go-olm (matrix E2EE) needs CGO; the release binaries are built with
  # the goolm (pure-Go) tag instead. Keep the same tag so the nix build
  # matches the shipped binaries and avoids CGO/olm native deps.
  tags = ["goolm"];

  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  meta = with lib; {
    description = "AI coding agent for the terminal with a polished TUI, resumable sessions, MCP integrations, built-in tools";
    homepage = "https://github.com/topcheer/ggcode";
    license = licenses.mit;
    mainProgram = "ggcode";
    platforms = ["x86_64-linux" "aarch64-linux"];
  };
}
