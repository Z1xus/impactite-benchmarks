# the official image behind a proxy, as its readme sets it up
# rate limits are lifted so it is not throttled where the others are not
KINDS=goatcounter
IMAGE=docker.io/arp242/goatcounter:2.7.0
VERSION=$IMAGE
IMAGES=$IMAGE
CONTAINERS=bench-goatcounter
VOLUMES=bench-goatcounter

up() {
	gc() { $D run --rm -v bench-goatcounter:/home/goatcounter/goatcounter-data "$IMAGE" "$@"; }
	gc db create site -createdb -vhost stats.bench.test -user.email bench@bench.test -user.password "$(random 16)" >/dev/null
	gc db create apitoken -user 1 -perm count -name bench >/dev/null
	# the cli can not grant the stats permission yet, 126 is every permission bit
	gc db query "update api_tokens set permissions = 126" >/dev/null
	KEY=$(gc db query "select token from api_tokens" | sed -n 2p | tr -d ' ')
	$D run -d --name bench-goatcounter --network bench -v bench-goatcounter:/home/goatcounter/goatcounter-data "$IMAGE" \
		serve -listen :8080 -tls proxy -automigrate -ratelimit count:1000000/1,api:1000000/1 >/dev/null
	URL=http://bench-goatcounter:8080
	HOST=stats.bench.test
	wait_http "$URL/status"
}
