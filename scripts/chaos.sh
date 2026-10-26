#!/usr/bin/env bash
# Chaos test: upload files through the real chunked upload API while Sendly pods
# are deleted at random, then check that every upload finalizes and downloads
# with the size that was uploaded.
#
#   BASE_URL=https://sendly.example.com NAMESPACE=sendly scripts/chaos.sh
#
# Needs kubectl (pointed at the cluster), curl and jq. The cluster is disturbed
# for real: run it against a test deployment, not production.
set -euo pipefail

BASE_URL=${BASE_URL:?set BASE_URL, e.g. https://sendly.example.com}
NAMESPACE=${NAMESPACE:-sendly}
JOBS=${JOBS:-3}
SIZE_MB=${SIZE_MB:-200}
KILL_EVERY=${KILL_EVERY:-15}      # seconds between pod deletions
KILL_PATTERN=${KILL_PATTERN:-'sendly'}
CHUNK=$((5 * 1024 * 1024))         # the browser's chunk size

work=$(mktemp -d)
chaos_pid=
cleanup() {
  local rc=$?
  set +e
  # The chaos loop's sleep would otherwise outlive the script.
  if [[ -n $chaos_pid ]]; then
    pkill -P "$chaos_pid" 2>/dev/null
    kill "$chaos_pid" 2>/dev/null
    wait "$chaos_pid" 2>/dev/null
  fi
  jobs -p | xargs -r kill 2>/dev/null
  rm -rf "$work"
  exit "$rc"
}
trap cleanup EXIT

# The API checks a double-submit CSRF token: any value works if cookie and header match.
CSRF=(-b csrf_token=chaos -H 'X-CSRF-Token: chaos')

# Retry like the browser (web/static/js/resilient-fetch.js): network errors and
# 408/425/429/499/5xx are waited out for up to 5 minutes.
req() { # tag method url [curl args...] -> body on stdout, status in $work/status-<tag>
  local tag=$1 method=$2 url=$3; shift 3
  local deadline=$((SECONDS + 300)) code
  while :; do
    code=$(curl -sS -o "$work/body-$tag" -w '%{http_code}' -X "$method" "${CSRF[@]}" "$@" "$url" 2>/dev/null) || code=000
    case $code in 000|408|425|429|499|5??) ;; *) echo "$code" >"$work/status-$tag"; cat "$work/body-$tag"; return 0;; esac
    ((SECONDS < deadline)) || { echo "$code" >"$work/status-$tag"; return 1; }
    sleep 2
  done
}
status() { cat "$work/status-$1"; }

run_upload() {
  local n=$1 start=$SECONDS
  local in="$work/in-$n.bin"
  head -c $((SIZE_MB * 1024 * 1024)) /dev/urandom >"$in"
  local size; size=$(stat -c %s "$in")
  local chunks=$(((size + CHUNK - 1) / CHUNK))

  local init sid
  init=$(req "$n" POST "$BASE_URL/api/upload/init" -H 'Content-Type: application/json' \
    -d "{\"file_name\":\"chaos-$n.bin\",\"file_size\":$size,\"total_chunks\":$chunks,\"chunk_size\":$CHUNK}")
  [[ $(status "$n") == 200 ]] || { echo "upload $n: init got $(status "$n")"; return 1; }
  sid=$(jq -r .session_id <<<"$init")

  for ((i = 0; i < chunks; i++)); do
    dd if="$in" of="$work/chunk-$n" bs="$CHUNK" skip="$i" count=1 2>/dev/null
    req "$n" POST "$BASE_URL/api/upload/chunk" -F "session_id=$sid" -F "chunk_index=$i" -F "chunk=@$work/chunk-$n" >/dev/null
    [[ $(status "$n") == 200 ]] || { echo "upload $n: chunk $i got $(status "$n")"; return 1; }
  done

  req "$n" POST "$BASE_URL/api/upload/complete" -H 'Content-Type: application/json' \
    -d "{\"session_id\":\"$sid\",\"confirmed\":true}" >/dev/null
  [[ $(status "$n") == 200 ]] || { echo "upload $n: complete got $(status "$n")"; return 1; }

  local st=""
  while [[ $st != done ]]; do
    st=$(req "$n" GET "$BASE_URL/api/upload/status/$sid" | jq -r .status 2>/dev/null || true)
    [[ $st == error:* ]] && { echo "upload $n: assembly failed: $st"; return 1; }
    ((SECONDS - start < 1800)) || { echo "upload $n: assembly timed out"; return 1; }
    sleep 2
  done

  local fin fid
  fin=$(req "$n" POST "$BASE_URL/api/upload/finalize" -H 'Content-Type: application/json' \
    -d "{\"session_id\":\"$sid\",\"duration\":\"7d\"}")
  [[ $(status "$n") == 200 ]] || { echo "upload $n: finalize got $(status "$n"): $fin"; return 1; }
  fid=$(jq -r .file_id <<<"$fin")

  local got
  got=$(curl -sS -o /dev/null -w '%{size_download}' --retry 30 --retry-all-errors --retry-delay 2 "$BASE_URL/api/file/$fid/download")
  [[ $got == "$size" ]] || { echo "upload $n: downloaded $got bytes, expected $size"; return 1; }
  echo "upload $n: OK ($size bytes, $((SECONDS - start))s)"
}

(
  while sleep "$KILL_EVERY"; do
    pod=$(kubectl -n "$NAMESPACE" get pods -o name | grep -E "$KILL_PATTERN" | shuf -n1 || true)
    [[ -n $pod ]] || continue
    echo "chaos: deleting $pod"
    kubectl -n "$NAMESPACE" delete "$pod" --wait=false >/dev/null || true
  done
) &
chaos_pid=$!

pids=()
for ((n = 1; n <= JOBS; n++)); do run_upload "$n" & pids+=($!); done
fail=0
for p in "${pids[@]}"; do wait "$p" || fail=1; done

if ((fail)); then echo "CHAOS TEST FAILED"; exit 1; fi
echo "all $JOBS uploads completed despite pod deletions"
