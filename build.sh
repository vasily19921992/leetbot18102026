#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

# Apply production fixes if patch is present (idempotent-ish: skip if already applied)
if [ -f main.go.patch ]; then
  if ! grep -q 'frame-ancestors' main.go 2>/dev/null; then
    echo "→ applying main.go.patch"
    patch -p0 < main.go.patch
    mkdir -p backend
    cp main.go backend/main.go
  else
    echo "→ main.go already patched"
  fi
fi

ensure_node() {
  if command -v node >/dev/null 2>&1; then
    local major
    major="$(node -v | sed 's/^v//' | cut -d. -f1)"
    if [ "$major" -ge 18 ]; then
      echo "→ using existing node $(node -v)"
      return
    fi
  fi
  local ver="v22.18.0"
  local arch
  case "$(uname -m)" in
    x86_64|amd64) arch="x64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) echo "unsupported arch: $(uname -m)"; exit 1 ;;
  esac
  local url="https://nodejs.org/dist/${ver}/node-${ver}-linux-${arch}.tar.xz"
  echo "→ downloading Node ${ver} (${arch})"
  mkdir -p .tools
  curl -fsSL "$url" -o .tools/node.tar.xz
  tar -xJf .tools/node.tar.xz -C .tools
  export PATH="$ROOT/.tools/node-${ver}-linux-${arch}/bin:$PATH"
  echo "→ node $(node -v), npm $(npm -v)"
}

ensure_node
echo "→ building frontend"
cd frontend
if [ -f package-lock.json ]; then
  npm ci
else
  npm install
fi
npm run build
cd "$ROOT"

echo "→ building Go binary"
go build -o leetri .
echo "✓ build complete: ./leetri"
