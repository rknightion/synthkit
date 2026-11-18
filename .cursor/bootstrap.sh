#!/usr/bin/env bash
set -euo pipefail

go_version=$(awk '$1 == "go" { print $2; exit }' go.mod)
node_version=$(awk '$1 == "node-version:" { print $2; exit }' .github/workflows/ci.yml)
tool_bin="${XDG_CACHE_HOME:-$HOME/.cache}/synthkit/cursor/bin"
export GOTOOLCHAIN="go$go_version" GOWORK=off GOPROXY=https://proxy.golang.org
export PATH="$tool_bin:$PATH"

if (( $# )); then
    exec fnm exec --using="$node_version" -- "$@"
fi

if ! fnm exec --using="$node_version" -- node --version >/dev/null 2>&1; then
    fnm install "$node_version"
fi
test "$(fnm exec --using="$node_version" -- node --version)" = "v$node_version"
test "$(go env GOVERSION)" = "$GOTOOLCHAIN"

mkdir -p "$tool_bin"
# Match the Helm pin in chart-e2e.yml. The arm64 checksum is from the same release.
helm_version=v4.2.4
if [[ "$("$tool_bin/helm" version --template '{{.Version}}' 2>/dev/null || true)" != "$helm_version" ]]; then
    case "$(go env GOHOSTARCH)" in
        amd64) arch=amd64; helm_sha=c306b46f719b0a4da32d0f78ee21bf90ce8d602f15b22ab753f0674d1670a7f3 ;;
        arm64) arch=arm64; helm_sha=564de2191b881e9f71b5606b25345821ea1682f06ab90499d3ab22b530176da1 ;;
        *) printf 'Unsupported architecture for Helm\n' >&2; exit 1 ;;
    esac
    stage=$(mktemp -d)
    trap 'rm -rf "$stage"' EXIT
    archive="helm-$helm_version-linux-$arch.tar.gz"
    curl -qfsSL --retry 2 --max-time 120 --proto '=https' --proto-redir '=https' \
        -o "$stage/$archive" "https://get.helm.sh/$archive"
    printf '%s  %s\n' "$helm_sha" "$stage/$archive" | sha256sum --check --strict -
    tar -xzf "$stage/$archive" -C "$stage" "linux-$arch/helm"
    install -m 0755 "$stage/linux-$arch/helm" "$tool_bin/helm"
fi

# lab-check requires Mike Farah yq v4.
yq_version=v4.53.6
if [[ "$("$tool_bin/yq" --version 2>/dev/null || true)" != *"version $yq_version" ]]; then
    GOBIN="$tool_bin" go install "github.com/mikefarah/yq/v4@$yq_version"
fi

npm_config_audit=false npm_config_fund=false fnm exec --using="$node_version" -- just setup ui build
go test -mod=readonly -c -o /dev/null ./...
