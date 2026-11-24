#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
image="${WEFT_SYNAPSE_IMAGE:-ghcr.io/element-hq/synapse@sha256:c6b5dba477c41b22167e771174456364b7c9c83a4cb2d5d7ee1e530334c5cd06}"
name="weft-test-$$"
volume="$name-data"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; docker volume rm "$volume" >/dev/null 2>&1 || true; }
trap cleanup EXIT
# Everything is isolated from existing Matrix accounts and servers.
docker volume create "$volume" >/dev/null
docker run --rm -v "$volume:/data" -e SYNAPSE_SERVER_NAME=weft.test -e SYNAPSE_REPORT_STATS=no "$image" generate >/dev/null
docker run --rm --entrypoint python -v "$volume:/data" "$image" -c '
import yaml
p="/data/homeserver.yaml"
c=yaml.safe_load(open(p))
c.update(enable_registration=True, enable_registration_without_verification=True,
         rc_registration={"per_second":1000,"burst_count":1000},
         rc_login={"address":{"per_second":1000,"burst_count":1000},"account":{"per_second":1000,"burst_count":1000}},
         rc_message={"per_second":1000,"burst_count":1000},
         rc_joins={"local":{"per_second":1000,"burst_count":1000},"remote":{"per_second":1000,"burst_count":1000}},
         suppress_key_server_warning=True)
open(p,"w").write(yaml.safe_dump(c))
'
docker run -d --name "$name" -v "$volume:/data" -p 127.0.0.1::8008 "$image" >/dev/null
address="http://$(docker port "$name" 8008/tcp)"
for i in $(seq 1 60); do
  if curl -fsS "$address/_matrix/client/versions" >/dev/null 2>&1; then break; fi
  sleep 1
done
WEFT_TEST_HOMESERVER="$address" go test -race -tags goolm,integration -v -timeout 5m ./internal/matrix
