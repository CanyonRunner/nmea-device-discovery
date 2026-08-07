# Module nmea-device-discovery

Inventories the devices on an NMEA 2000 network. The module watches the
device-identity PGNs — ISO Address Claim (60928), Product Information (126996),
and Configuration Information (126998) — and reports one entry per physical
device with its manufacturer, model, serial, installation details, current
source address, and a last-seen timestamp.

It is an analyzer/inventory tool for chart plotters, sensors, gateways, and
other nodes on the bus: query it every few minutes to see what is on the
network and when each device was last heard from.

The module holds no bus connection of its own. It reads through a
[viamboat](https://github.com/erh/viamboat) `boat-all-pgn` sensor configured on
the same machine, which must be connected to the NMEA 2000 network.

## Models

This module provides the following model(s):

- [`viam-soleng:nmea-device-discovery:devicelist`](viam-soleng_nmea-device-discovery_devicelist.md) - Sensor reporting one entry per NMEA 2000 device seen on the network
