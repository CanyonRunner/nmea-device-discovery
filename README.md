# Module nmea-device-discovery

Inventories the devices on an NMEA 2000 network. The module watches the device-identity PGNs — ISO Address Claim (60928), Product Information (126996), and Configuration Information (126998) — and reports one entry per physical device with its manufacturer, model, serial, installation details, current source address, and a last-seen timestamp.

It is an analyzer/inventory tool for chart plotters, sensors, gateways, and other nodes on the bus: query it every few minutes to see what is on the network and when each device was last heard from. With `track-instances` enabled it also breaks out what each device reports per instance (engine 0 and 1 behind an engine gateway, each tank, each battery bank), for monitoring UIs that need to address a component rather than the device that speaks for it.

The module holds no bus connection of its own. It reads through a [viamboat](https://github.com/erh/viamboat) `boat-all-pgn` sensor configured on the same machine, which must be connected to the NMEA 2000 network.

## Development

`go test ./...` covers the merge logic against synthetic readings and a dump
captured from a real network (`testdata/all-pgn-live.json`). To inspect the
model's output for any machine without deploying:

```sh
viam machines part run --part <part-id> --component all-pgn --method GetReadings > dump.json
go run ./cmd/cli -track-instances dump.json
```

## Models

This module provides the following model(s):

- [`viam-soleng:nmea-device-discovery:devicelist`](viam-soleng_nmea-device-discovery_devicelist.md) - Sensor reporting one entry per NMEA 2000 device seen on the network
