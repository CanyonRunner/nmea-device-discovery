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
newest address. A device with a non-zero device instance carries it in its
label as `... #<device instance>`, so the two halves of a dual gateway are
`... [340545]` and `... [340545] #1` whether or not both are currently on
the bus. Devices that still share a label and differ only by device
function (a plotter's virtual nodes, before their product info is seen) get
`... (<device function>)`. Entries are never pruned:
use the `last_seen` timestamp to decide what counts as stale for your
application. A device heard from only partially (for example, product info
without an address claim) is keyed by its source address as `... [src-<n>]`
until its claim is seen.

This model keeps no bus connection or state of its own; it reads through a
[viamboat](https://github.com/erh/viamboat) `boat-all-pgn` sensor on the same
machine, which accumulates the bus history. Inventory completeness is
therefore passive: devices appear once they claim an address or answer
requests on the bus. Query cadence of every few minutes is appropriate.

## Configuration

The following attribute template can be used to configure this model:

```json
{
  "all-pgn-component": <string>
}
```

### Attributes

The following attributes are available for this model:

| Name                | Type   | Inclusion | Description                                                                 |
|---------------------|--------|-----------|-----------------------------------------------------------------------------|
| `all-pgn-component` | string | Required  | Name of the viamboat `boat-all-pgn` sensor component to read the bus through |

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

Fields are best-effort per device: only what that device has transmitted on
the bus is present. Manufacturer codes known to the NMEA database render as
names ("Garmin"); unknown codes stay numeric. Key labels are sanitized
(fixed-length model strings are NUL-terminated with junk beyond the
terminator on some devices), but the `model_id` field keeps the raw decoded
value. `last_seen` has one-minute resolution.
