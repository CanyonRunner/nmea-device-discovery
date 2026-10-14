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

With `track-instances` enabled, each entry also lists the instanced data
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
  "track-instances": <boolean>
}
```

### Attributes

The following attributes are available for this model:

| Name                | Type    | Inclusion | Description                                                                 |
|---------------------|---------|-----------|-----------------------------------------------------------------------------|
| `all-pgn-component` | string  | Required  | Name of the viamboat `boat-all-pgn` sensor component to read the bus through |
| `track-instances`   | boolean | Optional  | Default `false`. When `true`, read the whole all-pgn history instead of only the identity PGNs, and add an `instances` list to each device (see below). |

`track-instances` widens what crosses the all-pgn hop from the three
identity PGNs to the full history (a few hundred entries on a typical
network). The call is in-process on the machine, so the cost is negligible;
the default stays off so existing readings keep their shape.

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
      "attributes": { "all-pgn-component": "all-pgn", "track-instances": true },
      "depends_on": ["all-pgn"]
    }
  ]
}
```

### Example Reading

```json
{
  "Garmin GPSMAP 8612 [1037921]": {
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
    "last_seen": "14 Oct 26 08:52 -0400",
    "src": 50,
    "unique_number": 88213,
    "manufacturer_code": "Yamaha",
    "device_function": "Engine Gateway",
    "device_class": "Propulsion",
    "model_id": "CL7 Gateway",
    "instances": [
      { "pgn": 127488, "instance": 0, "description": "Engine Parameters, Rapid Update", "last_seen": "14 Oct 26 08:52 -0400" },
      { "pgn": 127488, "instance": 1, "description": "Engine Parameters, Rapid Update", "last_seen": "14 Oct 26 08:52 -0400" },
      { "pgn": 127489, "instance": 0, "description": "Engine Parameters, Dynamic",      "last_seen": "14 Oct 26 08:52 -0400" },
      { "pgn": 127489, "instance": 1, "description": "Engine Parameters, Dynamic",      "last_seen": "14 Oct 26 08:51 -0400" },
      { "pgn": 127505, "instance": 2, "description": "Fluid Level",                     "last_seen": "14 Oct 26 08:50 -0400" }
    ]
  }
}
```

`instances` is sorted by PGN then instance and is present (possibly empty)
on every device when `track-instances` is on. The instance is taken from
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
