# impactite speaks both apis, so it runs once against each
KINDS="plausible goatcounter"
IMAGE=${IMPACTITE_IMAGE:-ghcr.io/z1xus/impactite:latest}
VERSION="$($D run --rm "$IMAGE" --version)"
IMAGES=$IMAGE
CONTAINERS=bench-impactite
VOLUMES=bench-impactite

up() {
	data="-v bench-impactite:/data"
	$D run --rm $data "$IMAGE" site add bench.test >/dev/null
	KEY=$($D run --rm $data "$IMAGE" key create bench)
	$D run -d --name bench-impactite --network bench $data "$IMAGE" serve >/dev/null
	URL=http://bench-impactite:8000
	HOST=
	wait_http "$URL/"
}
