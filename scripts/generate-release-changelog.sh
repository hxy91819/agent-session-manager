#!/usr/bin/env bash
set -euo pipefail

# Purpose:
#   Generate a release changelog from git commit subjects between the previous
#   semantic version tag and the requested release version.
#
# Parameters:
#   --version <tag>    Required release tag or ref, for example v0.3.1.
#   --output <path>    Required changelog output path.
#   --repo-url <url>   Optional repository URL used for compare links. Defaults
#                      to origin when it can be inferred.
#
# Outputs:
#   Writes a Markdown changelog to --output and prints a short summary to
#   stdout. Fails with a non-zero exit code for invalid input or missing refs.

usage() {
  cat <<'EOF'
Usage:
  scripts/generate-release-changelog.sh --version <tag> --output <path> [--repo-url <url>]

Description:
  Generates a Markdown release changelog from git commit subjects. The range is
  the previous semantic version tag reachable from <tag> through <tag>. If no
  previous semantic version tag exists, all commits reachable from <tag> are
  included.

Options:
  --version <tag>    Release tag or ref to describe, for example v0.3.1.
  --output <path>    Markdown file to write.
  --repo-url <url>   Repository URL for compare links. Defaults to origin when
                     origin can be converted to an HTTPS URL.
  -h, --help         Show this help text.

Outputs:
  - Writes a Markdown changelog to --output.
  - Prints the selected version, previous tag, and output path to stdout.
  - Exits non-zero when required arguments are missing or the version ref does
    not exist.

Examples:
  scripts/generate-release-changelog.sh --version v0.3.1 --output CHANGELOG.md
  scripts/generate-release-changelog.sh --version v0.3.1 --output /tmp/CHANGELOG.md --repo-url https://github.com/hxy91819/agent-session-manager
EOF
}

log_info() {
  echo "INFO: $*"
}

log_error() {
  echo "ERROR: $*" >&2
}

version=""
output=""
repo_url=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      if [[ $# -lt 2 || "$2" == --* ]]; then
        log_error "--version requires a value"
        exit 2
      fi
      version="$2"
      shift 2
      ;;
    --output)
      if [[ $# -lt 2 || "$2" == --* ]]; then
        log_error "--output requires a value"
        exit 2
      fi
      output="$2"
      shift 2
      ;;
    --repo-url)
      if [[ $# -lt 2 || "$2" == --* ]]; then
        log_error "--repo-url requires a value"
        exit 2
      fi
      repo_url="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      log_error "unknown argument: $1"
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${version}" ]]; then
  log_error "--version is required"
  exit 2
fi

if [[ -z "${output}" ]]; then
  log_error "--output is required"
  exit 2
fi

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  log_error "must be run inside a git work tree"
  exit 1
fi

if ! git rev-parse --verify --quiet "${version}^{commit}" >/dev/null; then
  log_error "version ref does not exist: ${version}"
  exit 1
fi

normalize_repo_url() {
  local raw="$1"
  if [[ -z "${raw}" ]]; then
    return 0
  fi

  if [[ "${raw}" =~ ^git@github.com:(.+)\.git$ ]]; then
    printf 'https://github.com/%s\n' "${BASH_REMATCH[1]}"
    return 0
  fi

  raw="${raw%.git}"
  printf '%s\n' "${raw}"
}

if [[ -z "${repo_url}" ]]; then
  origin_url="$(git config --get remote.origin.url || true)"
  repo_url="$(normalize_repo_url "${origin_url}")"
else
  repo_url="$(normalize_repo_url "${repo_url}")"
fi

previous_tag=""
while IFS= read -r tag; do
  if [[ "${tag}" != "${version}" ]]; then
    previous_tag="${tag}"
    break
  fi
done < <(git tag --sort=-v:refname --merged "${version}" 'v[0-9]*.[0-9]*.[0-9]*')

range="${version}"
range_label="Initial release"
compare_url=""
if [[ -n "${previous_tag}" ]]; then
  range="${previous_tag}..${version}"
  range_label="Changes since ${previous_tag}"
  if [[ -n "${repo_url}" ]]; then
    compare_url="${repo_url}/compare/${previous_tag}...${version}"
  fi
fi

mkdir -p "$(dirname "${output}")"

{
  printf '# %s\n\n' "${version}"
  printf '%s.\n\n' "${range_label}"

  if [[ -n "${compare_url}" ]]; then
    printf '[Full diff](%s)\n\n' "${compare_url}"
  fi

  printf '## Changes\n\n'
  if ! git log --no-merges --pretty=format:'- %s (%h)' "${range}"; then
    log_error "failed to collect commits for range: ${range}"
    exit 1
  fi
  printf '\n'
} >"${output}"

if ! grep -q '^- ' "${output}"; then
  {
    printf '# %s\n\n' "${version}"
    printf '%s.\n\n' "${range_label}"
    if [[ -n "${compare_url}" ]]; then
      printf '[Full diff](%s)\n\n' "${compare_url}"
    fi
    printf '## Changes\n\n'
    printf '- No commit changes found.\n'
  } >"${output}"
fi

log_info "version=${version}"
log_info "previous_tag=${previous_tag:-none}"
log_info "output=${output}"
