# Model viam-soleng:nmea-device-discovery:devicelist

A sensor that reports an inventory of the devices on an NMEA 2000 network.
Each reading returns one entry per physical device, merging that device's ISO
Address Claim (PGN 60928), Product Information (PGN 126996), and Configuration
Information (PGN 126998) into a single object.

Entries are keyed by a human-readable device label,
`<manufacturer> <model> [<unique number>]` (for example
`Simrad NSS4-12 iGPS [1589637]`). Devices are identified by their ISO NAME
from the address claim (unique number, manufacturer, device instance and
device function), not by bus address — NMEA 2000 addresses can move when
devices contend, and a device that changed address is reported once, at its
newest address. Distinct devices can share a label: a dual gateway claims
two addresses that differ only by device instance, and a plotter's virtual
nodes differ by device function. Such a group gets the shortest qualifier
that tells its members apart, `... #<device instance>`, then
`... (<device function>)`, then `... (src <n>)`. Entries are never pruned:
use the `last_seen` timestamp to decide what counts as stale for your
application. A device heard from only partially (for example, product info
without an address claim) is keyed by its source address as `... [src-<n>]`
until its claim is seen.

The model remembers what it has seen. Under `VIAM_MODULE_DATA` (a directory
viam-server provides to every module, which survives restarts and module
updates) it keeps a small JSON file keyed by device identity, so each entry
carries a `first_seen` that survives restarts, and a device that has left
the bus stays listed with its last `last_seen` instead of vanishing when
viamboat restarts with an empty history. Product info the bus has not
repeated since a restart is filled in from the file. The file is written
only when something changed, atomically. Set `persist` to `false` to turn
this off; use the `forget` and `reset` commands (below) to drop devices
that were genuinely removed from the boat.

By default (`track-instances`), each entry also lists the instanced data
the device transmits: one `instances` element per (PGN, instance) pair, so
an engine gateway reporting two engines shows Engine Parameters for
instance 0 and 1, a tank sender its tanks, a battery monitor its banks.
This is what a per-component alert or monitoring UI needs in order to offer
"engine 1" rather than just "the engine gateway".

This model keeps no bus connection or state of its own; it reads through a
[viamboat](https://github.com/erh/viamboat) `boat-all-pgn` sensor on the same
machine, which accumulates the bus history. Inventory completeness is
therefore passive: devices appear once they claim an address or answer
requests on the bus. Query cadence of every few minutes is appropriate.

## Configuration

The following attribute template can be used to configure this model:

```json
{
  "all-pgn-component": <string>,
  "track-instances": <boolean>,
  "persist": <boolean>
}
```

### Attributes

The following attributes are available for this model:

| Name                | Type    | Inclusion | Description                                                                 |
|---------------------|---------|-----------|-----------------------------------------------------------------------------|
| `all-pgn-component` | string  | Required  | Name of the viamboat `boat-all-pgn` sensor component to read the bus through |
| `track-instances`   | boolean | Optional  | Default `true`: read the whole all-pgn history and add an `instances` list to each device (see below). Set `false` to read only the identity PGNs and omit the list. |
| `persist`           | boolean | Optional  | Default `true`: remember devices in `$VIAM_MODULE_DATA/<component name>-devices.json` so `first_seen` and offline devices survive restarts. Set `false` to report only what the current all-pgn history holds. |

With `track-instances` on, the whole all-pgn history crosses the hop (a few
hundred entries on a typical network) instead of the three identity PGNs.
The call is in-process on the machine, so the cost is negligible. Set it to
`false` to get the upstream module's leaner readings.

### Example Configuration

A complete machine configuration needs the viamboat reader-backed `boat-all-pgn`
sensor plus this model depending on it:

```json
{
  "components": [
    {
      "name": "all-pgn",
      "api": "rdk:component:sensor",
      "model": "viam-labs:viamboat:boat-all-pgn",
      "attributes": { "reader": "can0" }
    },
    {
      "name": "devicelist",
      "api": "rdk:component:sensor",
      "model": "viam-soleng:nmea-device-discovery:devicelist",
      "attributes": { "all-pgn-component": "all-pgn" },
      "depends_on": ["all-pgn"]
    }
  ]
}
```

### Example Reading

```json
{
  "Garmin GPSMAP 8612 [1037921]": {
    "first_seen": "02 Aug 26 16:40 +0000",
    "last_seen": "07 Aug 26 09:12 +0000",
    "src": 3,
    "unique_number": 1037921,
    "manufacturer_code": "Garmin",
    "device_function": 130,
    "device_class": "Navigation",
    "model_id": "GPSMAP 8612",
    "product_code": 27716,
    "software_version_code": "9.10",
    "installation_description_1": "helm"
  },
  "999 CR Box [12345]": {
    "first_seen": "07 Aug 26 09:10 +0000",
    "last_seen": "07 Aug 26 09:10 +0000",
    "src": 42,
    "unique_number": 12345,
    "manufacturer_code": 999,
    "model_id": "CR Box"
  }
}
```

### Example Reading with `track-instances`

```json
{
  "Yamaha CL7 Gateway [88213]": {
    "first_seen": "03 Jun 26 11:20 -0400",
    "last_seen": "14 Oct 26 08:52 -0400",
    "src": 50,
    "unique_number": 88213,
    "manufacturer_code": "Yamaha",
    "device_function": "Engine Gateway",
    "device_class": "Propulsion",
    "model_id": "CL7 Gateway",
    "instances": [
      { "pgn": 127488, "instance": 0, "description": "Engine Parameters, Rapid Update", "first_seen": "03 Jun 26 11:20 -0400", "last_seen": "14 Oct 26 08:52 -0400" },
      { "pgn": 127488, "instance": 1, "description": "Engine Parameters, Rapid Update", "first_seen": "03 Jun 26 11:20 -0400", "last_seen": "14 Oct 26 08:52 -0400" },
      { "pgn": 127489, "instance": 0, "description": "Engine Parameters, Dynamic",      "first_seen": "03 Jun 26 11:20 -0400", "last_seen": "14 Oct 26 08:52 -0400" },
      { "pgn": 127489, "instance": 1, "description": "Engine Parameters, Dynamic",      "first_seen": "03 Jun 26 11:20 -0400", "last_seen": "14 Oct 26 08:51 -0400" },
      { "pgn": 127505, "instance": 2, "description": "Fluid Level",                     "first_seen": "21 Sep 26 15:02 -0400", "last_seen": "14 Oct 26 08:50 -0400" }
    ]
  }
}
```

`instances` is sorted by PGN then instance and is present (possibly empty)
on every device unless `track-instances` is `false`. Each instance carries
its own `first_seen` and `last_seen`, so a second engine or a new tank that
appears months after the gateway is dated on its own. The instance is taken from
the all-pgn key (`<pgn>-<src>-<instance>`), or from the message's
`Instance` field when the key has no instance segment; messages without
an instance (position, heading, ...) are not listed. A device that has
only been heard through instanced data, with no identity PGNs yet, appears
as `src-<n>` with its instances. Instanced traffic is sent far more often
than address claims, so with tracking on a device's `last_seen` reflects
its newest instanced message as well.

Fields are best-effort per device: only what that device has transmitted on
the bus is present. Manufacturer codes known to the NMEA database render as
names ("Garmin"); unknown codes stay numeric. Key labels are sanitized
(fixed-length model strings are NUL-terminated with junk beyond the
terminator on some devices), but the `model_id` field keeps the raw decoded
value. `last_seen` has one-minute resolution.

### first_seen

`first_seen` is the earliest bus timestamp the model has for the device:
on first sight, the oldest timestamp across its entries in the all-pgn
history (which can predate this component's install if viamboat was
already running), and from then on the value remembered in the state
file. It is tied to the device's identity, not its label, so it does not
restart when product info arrives and the label grows. A source without
an address claim (`src-<n>`) reports a `first_seen` but is not remembered,
since a bus address is not an identity.

### DoCommand

| Command | Arguments | Effect |
|---------|-----------|--------|
| `{"command": "devices"}` | | Same as Readings. |
| `{"command": "forget", "device": "<reading key>"}` | or `"identity"` | Drop one remembered device (for example after physically removing it). It reappears, with a new `first_seen`, if it is heard again. |
| `{"command": "reset"}` | | Forget every remembered device and delete the state file. |
| `{"command": "status"}` | | Effective settings, state file path, number of remembered devices. |
