package nmeadevicediscovery

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"go.viam.com/rdk/components/sensor"
	"go.viam.com/test"
)

// filteringAllPgn is a fakeAllPgn that, like the real viamboat sensor,
// honors the "pgns" filter in extra. Used where the test depends on what the
// filter lets through.
type filteringAllPgn struct {
	sensor.Sensor
	readings map[string]interface{}
	gotExtra map[string]interface{}
}

func (f *filteringAllPgn) Readings(ctx context.Context, extra map[string]interface{}) (map[string]interface{}, error) {
	f.gotExtra = extra
	pgns, _ := extra["pgns"].([]interface{})
	if len(pgns) == 0 {
		return f.readings, nil
	}
	want := map[int]bool{}
	for _, p := range pgns {
		if n, ok := asInt(p); ok {
			want[n] = true
		}
	}
	out := map[string]interface{}{}
	for k, v := range f.readings {
		if pgn, ok := asInt(v.(map[string]interface{})["pgn"]); ok && want[pgn] {
			out[k] = v
		}
	}
	return out, nil
}

func loadFixture(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	b, err := os.ReadFile(path)
	test.That(t, err, test.ShouldBeNil)
	var m map[string]interface{}
	test.That(t, json.Unmarshal(b, &m), test.ShouldBeNil)
	if inner, ok := m["readings"].(map[string]interface{}); ok {
		return inner
	}
	return m
}

func instancesOf(t *testing.T, got map[string]interface{}, key string) []map[string]interface{} {
	t.Helper()
	dev, ok := got[key].(map[string]interface{})
	test.That(t, ok, test.ShouldBeTrue)
	raw, ok := dev["instances"].([]interface{})
	test.That(t, ok, test.ShouldBeTrue)
	out := make([]map[string]interface{}, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(map[string]interface{}))
	}
	return out
}

// A twin-engine gateway: one source address, Engine Parameters for instance
// 0 and 1, keyed by viamboat as "<pgn>-<src>-<instance>".
func twinEngineReadings() map[string]interface{} {
	return map[string]interface{}{
		"60928-50": entry(60928, 50, "14 Oct 26 07:00 -0400", map[string]interface{}{
			"Unique Number": float64(88213), "Manufacturer Code": "Yamaha",
			"Device Function": "Engine Gateway", "Device Class": "Propulsion",
		}),
		"126996-50": entry(126996, 50, "14 Oct 26 07:00 -0400", map[string]interface{}{
			"Model ID": "CL7 Gateway",
		}),
		"127488-50-0": entry(127488, 50, "14 Oct 26 08:52 -0400", map[string]interface{}{
			"Instance": float64(0), "Speed": float64(3120), "description": "Engine Parameters, Rapid Update",
		}),
		"127488-50-1": entry(127488, 50, "14 Oct 26 08:52 -0400", map[string]interface{}{
			"Instance": float64(1), "Speed": float64(3095), "description": "Engine Parameters, Rapid Update",
		}),
		"127489-50-1": entry(127489, 50, "14 Oct 26 08:51 -0400", map[string]interface{}{
			"Instance": float64(1), "description": "Engine Parameters, Dynamic",
		}),
		"127489-50-0": entry(127489, 50, "14 Oct 26 08:52 -0400", map[string]interface{}{
			"Instance": float64(0), "description": "Engine Parameters, Dynamic",
		}),
		// Fluid Level with the instance only in the payload (2-segment key).
		"127505-50": entry(127505, 50, "14 Oct 26 08:50 -0400", map[string]interface{}{
			"Instance": float64(2), "Type": "Fuel", "description": "Fluid Level",
		}),
		// Not instanced: must not appear in the list.
		"129025-50": entry(129025, 50, "14 Oct 26 08:52 -0400", map[string]interface{}{
			"Latitude": 41.0, "description": "Position, Rapid Update",
		}),
	}
}

func boolPtr(b bool) *bool { return &b }

func TestTrackInstancesOffKeepsFilterAndShape(t *testing.T) {
	fake := &filteringAllPgn{readings: twinEngineReadings()}
	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake, cfg: &Config{AllPGNComponent: "all-pgn", TrackInstances: boolPtr(false)}}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	// Explicitly off: identity filter pushed down, no instances field.
	test.That(t, fake.gotExtra["pgns"], test.ShouldResemble,
		[]interface{}{pgnAddressClaim, pgnProductInfo, pgnConfigInfo})
	dev := got["Yamaha CL7 Gateway [88213]"].(map[string]interface{})
	_, has := dev["instances"]
	test.That(t, has, test.ShouldBeFalse)
	test.That(t, dev["last_seen"], test.ShouldEqual, "14 Oct 26 07:00 -0400")
}

func TestTrackInstancesDefaultsOn(t *testing.T) {
	fake := &filteringAllPgn{readings: twinEngineReadings()}
	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake, cfg: &Config{AllPGNComponent: "all-pgn"}}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	_, filtered := fake.gotExtra["pgns"]
	test.That(t, filtered, test.ShouldBeFalse)
	test.That(t, len(instancesOf(t, got, "Yamaha CL7 Gateway [88213]")), test.ShouldEqual, 5)
}

func TestTrackInstancesBreaksOutGatewayInstances(t *testing.T) {
	fake := &filteringAllPgn{readings: twinEngineReadings()}
	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake, cfg: &Config{AllPGNComponent: "all-pgn", TrackInstances: boolPtr(true)}}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	// The read is widened: no pgns filter.
	_, filtered := fake.gotExtra["pgns"]
	test.That(t, filtered, test.ShouldBeFalse)

	// Still one device, with one entry per (PGN, instance), sorted.
	test.That(t, len(got), test.ShouldEqual, 1)
	inst := instancesOf(t, got, "Yamaha CL7 Gateway [88213]")
	test.That(t, len(inst), test.ShouldEqual, 5)
	test.That(t, inst[0]["pgn"], test.ShouldEqual, 127488)
	test.That(t, inst[0]["instance"], test.ShouldEqual, 0)
	test.That(t, inst[1]["pgn"], test.ShouldEqual, 127488)
	test.That(t, inst[1]["instance"], test.ShouldEqual, 1)
	test.That(t, inst[1]["description"], test.ShouldEqual, "Engine Parameters, Rapid Update")
	test.That(t, inst[2]["pgn"], test.ShouldEqual, 127489)
	test.That(t, inst[2]["instance"], test.ShouldEqual, 0)
	test.That(t, inst[3]["pgn"], test.ShouldEqual, 127489)
	test.That(t, inst[3]["instance"], test.ShouldEqual, 1)
	test.That(t, inst[3]["last_seen"], test.ShouldEqual, "14 Oct 26 08:51 -0400")
	// Instance read from the payload when the key has no instance segment.
	test.That(t, inst[4]["pgn"], test.ShouldEqual, 127505)
	test.That(t, inst[4]["instance"], test.ShouldEqual, 2)

	// Instanced traffic is the liveness signal: last_seen moved past the
	// 07:00 address claim to the newest engine message.
	dev := got["Yamaha CL7 Gateway [88213]"].(map[string]interface{})
	test.That(t, dev["last_seen"], test.ShouldEqual, "14 Oct 26 08:52 -0400")
}

func TestTrackInstancesDeviceWithoutClaim(t *testing.T) {
	// Battery data from an address we have no identity PGNs for yet.
	fake := &filteringAllPgn{readings: map[string]interface{}{
		"127508-7-0": entry(127508, 7, "14 Oct 26 08:52 -0400", map[string]interface{}{
			"Instance": float64(0), "Voltage": 13.1, "description": "Battery Status",
		}),
	}}
	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake, cfg: &Config{TrackInstances: boolPtr(true)}}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	inst := instancesOf(t, got, "src-7")
	test.That(t, len(inst), test.ShouldEqual, 1)
	test.That(t, inst[0]["pgn"], test.ShouldEqual, 127508)
	dev := got["src-7"].(map[string]interface{})
	test.That(t, dev["src"], test.ShouldEqual, 7)
	test.That(t, dev["last_seen"], test.ShouldEqual, "14 Oct 26 08:52 -0400")
}

func TestTrackInstancesFollowNewestAddress(t *testing.T) {
	// Same device claimed 42 then moved to 43; the history still holds the
	// tank it reported from 42. Only the current address's instances count.
	fake := &filteringAllPgn{readings: map[string]interface{}{
		"60928-42": entry(60928, 42, "14 Oct 26 07:00 -0400", map[string]interface{}{
			"Unique Number": float64(12345), "Manufacturer Code": float64(999),
		}),
		"127505-42-1": entry(127505, 42, "14 Oct 26 07:05 -0400", map[string]interface{}{
			"Instance": float64(1), "description": "Fluid Level",
		}),
		"60928-43": entry(60928, 43, "14 Oct 26 08:00 -0400", map[string]interface{}{
			"Unique Number": float64(12345), "Manufacturer Code": float64(999),
		}),
		"127505-43-1": entry(127505, 43, "14 Oct 26 08:52 -0400", map[string]interface{}{
			"Instance": float64(1), "description": "Fluid Level",
		}),
	}}
	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake, cfg: &Config{TrackInstances: boolPtr(true)}}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	test.That(t, len(got), test.ShouldEqual, 1)
	inst := instancesOf(t, got, "999 [12345]")
	test.That(t, len(inst), test.ShouldEqual, 1)
	test.That(t, inst[0]["last_seen"], test.ShouldEqual, "14 Oct 26 08:52 -0400")
}

// Dump captured from a real network (Garmin, Furuno, Actisense, Airmar,
// Vesper, ...) with `viam machines part run ... --method GetReadings`.
func TestTrackInstancesLiveFixture(t *testing.T) {
	fake := &filteringAllPgn{readings: loadFixture(t, "testdata/all-pgn-live.json")}
	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake, cfg: &Config{TrackInstances: boolPtr(true)}}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	withInstances := 0
	for _, v := range got {
		if len(v.(map[string]interface{})["instances"].([]interface{})) > 0 {
			withInstances++
		}
	}
	test.That(t, withInstances, test.ShouldEqual, 9)

	// The autopilot reports its rudder, the gateway a battery bank.
	rudder := instancesOf(t, got, "Garmin Reactor 40 SBW [1944315]")
	test.That(t, len(rudder), test.ShouldEqual, 1)
	test.That(t, rudder[0]["pgn"], test.ShouldEqual, 127245)
	test.That(t, rudder[0]["description"], test.ShouldEqual, "Rudder")

	// The dual gateway claims two addresses under one unique number, told
	// apart by device instance; only one of them reports the battery.
	test.That(t, len(got), test.ShouldEqual, 32)
	battery := instancesOf(t, got, "Actisense NGX-1: Dual Gateway [340545] #0")
	test.That(t, len(battery), test.ShouldEqual, 1)
	test.That(t, battery[0]["pgn"], test.ShouldEqual, 127508)
	test.That(t, battery[0]["instance"], test.ShouldEqual, 0)
	other := instancesOf(t, got, "Actisense NGX-1: Dual Gateway [340545] #1")
	test.That(t, len(other), test.ShouldEqual, 0)

	// Address claims are not instances.
	for key := range got {
		for _, inst := range instancesOf(t, got, key) {
			test.That(t, inst["pgn"], test.ShouldNotEqual, pgnAddressClaim)
		}
	}
}

func TestVirtualNodesWithSharedUniqueNumberAreDistinct(t *testing.T) {
	// A Garmin plotter exposes a Display node and a Button Interface node
	// under the same unique number, both live. Neither may hide the other,
	// and the one streaming temperature keeps its instance.
	fake := &filteringAllPgn{readings: map[string]interface{}{
		"60928-17": entry(60928, 17, "14 Oct 26 08:52 -0400", map[string]interface{}{
			"Unique Number": float64(474370), "Manufacturer Code": "Garmin", "Device Function": "Display",
		}),
		"126996-17": entry(126996, 17, "14 Oct 26 08:52 -0400", map[string]interface{}{"Model ID": "GPSMAP 9500"}),
		"130312-17-0": entry(130312, 17, "14 Oct 26 08:52 -0400", map[string]interface{}{
			"Instance": float64(0), "description": "Temperature",
		}),
		"60928-9": entry(60928, 9, "14 Oct 26 08:52 -0400", map[string]interface{}{
			"Unique Number": float64(474370), "Manufacturer Code": "Garmin", "Device Function": "Button Interface",
		}),
		"126996-9": entry(126996, 9, "14 Oct 26 08:52 -0400", map[string]interface{}{"Model ID": "Virtual N2K Input Handler"}),
	}}
	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake, cfg: &Config{TrackInstances: boolPtr(true)}}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	test.That(t, len(got), test.ShouldEqual, 2)
	temp := instancesOf(t, got, "Garmin GPSMAP 9500 [474370]")
	test.That(t, len(temp), test.ShouldEqual, 1)
	handler := instancesOf(t, got, "Garmin Virtual N2K Input Handler [474370]")
	test.That(t, len(handler), test.ShouldEqual, 0)
}

func TestCollidingLabelsGetShortestQualifier(t *testing.T) {
	// Same product, same unique number, no product info yet: the two halves
	// differ by device instance, and the two nodes of one by function.
	claim := func(src, lower, upper float64, fn string) map[string]interface{} {
		return entry(60928, src, "14 Oct 26 08:52 -0400", map[string]interface{}{
			"Unique Number": float64(777), "Manufacturer Code": "Actisense", "Device Function": fn,
			"Device Instance Lower": lower, "Device Instance Upper": upper,
		})
	}
	fake := &filteringAllPgn{readings: map[string]interface{}{
		"60928-0": claim(0, 0, 0, "PC Gateway"),
		"60928-2": claim(2, 1, 0, "PC Gateway"),
		"60928-4": claim(4, 1, 0, "Display"),
		"60928-6": claim(6, 1, 0, "Display"),
	}}
	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake, cfg: &Config{}}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	// Four claims, three distinct identities (4 and 6 are one device that
	// moved), told apart by instance, then function.
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	test.That(t, len(keys), test.ShouldEqual, 3)
	_, ok := got["Actisense [777] #0 (PC Gateway)"]
	test.That(t, ok, test.ShouldBeTrue)
	_, ok = got["Actisense [777] #1 (PC Gateway)"]
	test.That(t, ok, test.ShouldBeTrue)
	display, ok := got["Actisense [777] #1 (Display)"].(map[string]interface{})
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, display["src"], test.ShouldEqual, 4) // tie on last_seen: lower address wins
}
