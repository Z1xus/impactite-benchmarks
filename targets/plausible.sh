# the community edition as its compose file sets it up, with its clickhouse config
KINDS=plausible
IMAGE=ghcr.io/plausible/community-edition:v3.2.1
POSTGRES=docker.io/library/postgres:16-alpine
CLICKHOUSE=docker.io/clickhouse/clickhouse-server:24.12-alpine
VERSION=$IMAGE
IMAGES="$IMAGE $POSTGRES $CLICKHOUSE"
CONTAINERS="bench-plausible bench-plausible-db bench-plausible-events"
VOLUMES="bench-plausible bench-plausible-db bench-plausible-events"

up() {
	config=$PWD/targets/plausible
	$D run -d --name bench-plausible-db --network bench -v bench-plausible-db:/var/lib/postgresql/data \
		-e POSTGRES_PASSWORD=postgres "$POSTGRES" >/dev/null
	$D run -d --name bench-plausible-events --network bench --ulimit nofile=262144:262144 \
		-v bench-plausible-events:/var/lib/clickhouse \
		-v "$config/logs.xml:/etc/clickhouse-server/config.d/logs.xml:ro,z" \
		-v "$config/ipv4-only.xml:/etc/clickhouse-server/config.d/ipv4-only.xml:ro,z" \
		-v "$config/low-resources.xml:/etc/clickhouse-server/config.d/low-resources.xml:ro,z" \
		-v "$config/default-profile-low-resources-overrides.xml:/etc/clickhouse-server/users.d/default-profile-low-resources-overrides.xml:ro,z" \
		-e CLICKHOUSE_SKIP_USER_SETUP=1 "$CLICKHOUSE" >/dev/null
	until $D exec bench-plausible-db pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
	wait_http http://bench-plausible-events:8123/ping

	env="-e BASE_URL=http://bench-plausible:8000 -e SECRET_KEY_BASE=$(random 48) -e TMPDIR=/var/lib/plausible/tmp
		-e DATABASE_URL=postgres://postgres:postgres@bench-plausible-db:5432/plausible_db
		-e CLICKHOUSE_DATABASE_URL=http://bench-plausible-events:8123/plausible_events_db"
	$D run -d --name bench-plausible --network bench --ulimit nofile=65535:65535 -v bench-plausible:/var/lib/plausible $env \
		"$IMAGE" sh -c "/entrypoint.sh db createdb && /entrypoint.sh db migrate && /entrypoint.sh run" >/dev/null
	URL=http://bench-plausible:8000
	HOST=
	wait_http "$URL/api/health"

	KEY=$(random 32)
	password=$(random 16)
	$D exec bench-plausible /app/bin/plausible rpc "
		{:ok, user} = Plausible.Repo.insert(Plausible.Auth.User.new(%{email: \"bench@bench.test\", name: \"bench\", password: \"$password\", password_confirmation: \"$password\"}))
		{:ok, _} = Plausible.Sites.create(user, %{\"domain\" => \"bench.test\", \"timezone\" => \"Etc/UTC\"})
		{:ok, team} = Plausible.Teams.get_or_create(user)
		{:ok, _} = Plausible.Auth.create_stats_api_key(user, team, \"bench\", \"$KEY\")
		Plausible.Site.Cache.refresh_all()
	" >/dev/null
}
