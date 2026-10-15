# impactite-benchmarks

Compare how fast self-hosted analytics tools accept pageviews and answer queries, and how much CPU, memory and disk space they use.

The suite runs Impactite, Plausible CE and GoatCounter one at a time on the same machine, using the same generated traffic. Impactite runs with both APIs so you can compare it with either tool.

## Run

You need Docker or Podman to build and run the suite in containers.

```sh
./run.sh                     # all tools
./run.sh plausible           # one tool
DOCKER=podman ./run.sh        # use Podman
EVENTS=200000 RUNS=20 ./run.sh # smaller run
```

By default, each run sends 1M pageviews over 32 connections and measures each query 50 times. Use the EVENTS, CONCURRENCY and RUNS environment variables to change these settings.

The script creates and removes its own containers and volumes, so keep names starting with bench- free. It opens no host ports. CPU measurements require Linux with access to the container cgroups.

## Results

The script prints a table and saves raw results with machine details in [results](results).

It measures:

- Pageviews per second, request latency and failures.
- Stored counts and time until they settle.
- Query latency after three warmups.
- Idle and active CPU use, memory, disk, image and compressed tracker size.

Compare stored counts within the same API: Plausible counts pageviews, while GoatCounter counts a visitor once per page. Disk measurements cover one open day. The load generator shares CPU with the tool, which can affect throughput.

## Sources

You can check each tool's setup in [targets](targets) and the traffic and queries in [runner/main.go](runner/main.go).

Projects: [Impactite](https://github.com/Z1xus/impactite), [Plausible CE](https://github.com/plausible/community-edition), [GoatCounter](https://github.com/arp242/goatcounter).
