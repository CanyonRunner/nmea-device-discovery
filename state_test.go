package nmeadevicediscovery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"go.viam.com/rdk/components/sensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/test"
)

// newPersisting builds the sensor through NewDevicelist so it loads and
// saves state under VIAM_MODULE_DATA (pointed at dir by the caller).
func newPersisting(t *testing.T, fake sensor.Sensor, cfg *Config) sensor.Sensor {
	t.Helper()
	deps := resource.Dependencies{sensor.Named("all-pgn"): fake}
	if cfg == nil {
		cfg = &Config{}
	}
	cfg.AllPGNComponent = "all-pgn"
	s, err := NewDevicelist(context.Background(), deps, sensor.Named("devicelist"), cfg, logging.NewTestLogger(t))
	test.That(t, err, test.ShouldBeNil)
	return s
}

func device(t *testing.T, got map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	dev, ok := got[key].(map[string]interface{})
	if !ok {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		t.Fatalf("no device %q in %v", key, keys)
	}
	return dev
}

func TestFirstSeenIsEarliestBusTimestamp(t *testing.T) {
	fake := &filteringAllPgn{readings: twinEngineReadings()}
	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	// Claim at 07:00, engine data through 08:52: the window is the whole span.
	dev := device(t, got, "Yamaha CL7 Gateway [88213]")
	test.That(t, dev["first_seen"], test.ShouldEqual, "14 Oct 26 07:00 -0400")
	test.That(t, dev["last_seen"], test.ShouldEqual, "14 Oct 26 08:52 -0400")

	// An instance's first_seen starts at the timestamp it was first noticed with.
	inst := instancesOf(t, got, "Yamaha CL7 Gateway [88213]")
	test.That(t, inst[3]["pgn"], test.ShouldEqual, 127489)
	test.That(t, inst[3]["instance"], test.ShouldEqual, 1)
	test.That(t, inst[3]["first_seen"], test.ShouldEqual, "14 Oct 26 08:51 -0400")
}

func TestStateSurvivesRestartAndKeepsOfflineDevices(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VIAM_MODULE_DATA", dir)
	statePath := filepath.Join(dir, "devicelist-devices.json")

	// Day 1: gateway with two engines on the bus.
	s := newPersisting(t, &filteringAllPgn{readings: twinEngineReadings()}, nil)
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, device(t, got, "Yamaha CL7 Gateway [88213]")["first_seen"], test.ShouldEqual, "14 Oct 26 07:00 -0400")
	_, err = os.Stat(statePath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, s.Close(context.Background()), test.ShouldBeNil)

	// Day 2: viamboat restarted, the gateway is unplugged, a new depth sensor
	// claims an address. The all-pgn history knows nothing about the gateway.
	day2 := map[string]interface{}{
		"60928-35": entry(60928, 35, "15 Oct 26 09:00 -0400", map[string]interface{}{
			"Unique Number": float64(780712), "Manufacturer Code": "Airmar", "Device Function": "Depth / Speed / Temp",
		}),
	}
	s = newPersisting(t, &filteringAllPgn{readings: day2}, nil)
	got, err = s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(got), test.ShouldEqual, 2)

	// The gateway is still listed, unchanged, with both engines remembered.
	gw := device(t, got, "Yamaha CL7 Gateway [88213]")
	test.That(t, gw["first_seen"], test.ShouldEqual, "14 Oct 26 07:00 -0400")
	test.That(t, gw["last_seen"], test.ShouldEqual, "14 Oct 26 08:52 -0400")
	test.That(t, gw["model_id"], test.ShouldEqual, "CL7 Gateway")
	test.That(t, gw["src"], test.ShouldEqual, 50)
	test.That(t, len(instancesOf(t, got, "Yamaha CL7 Gateway [88213]")), test.ShouldEqual, 5)

	// The new device gets today's first_seen.
	test.That(t, device(t, got, "Airmar [780712]")["first_seen"], test.ShouldEqual, "15 Oct 26 09:00 -0400")
	test.That(t, s.Close(context.Background()), test.ShouldBeNil)

	// Day 3: gateway back, now at a different address, with only engine 0
	// running and its product info not yet re-broadcast. first_seen holds,
	// last_seen moves, the file's product info and engine 1 are kept.
	day3 := map[string]interface{}{
		"60928-51": entry(60928, 51, "16 Oct 26 10:00 -0400", map[string]interface{}{
			"Unique Number": float64(88213), "Manufacturer Code": "Yamaha",
			"Device Function": "Engine Gateway", "Device Class": "Propulsion",
		}),
		"127488-51-0": entry(127488, 51, "16 Oct 26 10:05 -0400", map[string]interface{}{
			"Instance": float64(0), "description": "Engine Parameters, Rapid Update",
		}),
	}
	s = newPersisting(t, &filteringAllPgn{readings: day3}, nil)
	got, err = s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	gw = device(t, got, "Yamaha CL7 Gateway [88213]")
	test.That(t, gw["first_seen"], test.ShouldEqual, "14 Oct 26 07:00 -0400")
	test.That(t, gw["last_seen"], test.ShouldEqual, "16 Oct 26 10:05 -0400")
	test.That(t, gw["src"], test.ShouldEqual, 51)
	test.That(t, gw["model_id"], test.ShouldEqual, "CL7 Gateway")
	inst := instancesOf(t, got, "Yamaha CL7 Gateway [88213]")
	test.That(t, len(inst), test.ShouldEqual, 5)
	test.That(t, inst[0]["instance"], test.ShouldEqual, 0)
	test.That(t, inst[0]["first_seen"], test.ShouldEqual, "14 Oct 26 08:52 -0400")
	test.That(t, inst[0]["last_seen"], test.ShouldEqual, "16 Oct 26 10:05 -0400")
	test.That(t, inst[1]["instance"], test.ShouldEqual, 1)
	test.That(t, inst[1]["last_seen"], test.ShouldEqual, "14 Oct 26 08:52 -0400")
}

func TestStateSavesOnlyWhenChanged(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VIAM_MODULE_DATA", dir)
	statePath := filepath.Join(dir, "devicelist-devices.json")

	s := newPersisting(t, &filteringAllPgn{readings: twinEngineReadings()}, nil)
	_, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	before, err := os.Stat(statePath)
	test.That(t, err, test.ShouldBeNil)

	// Same history again: nothing changed, the file is not rewritten.
	_, err = s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	after, err := os.Stat(statePath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, after.ModTime(), test.ShouldEqual, before.ModTime())

	// And the file is keyed by identity, not label.
	var st persistedState
	b, err := os.ReadFile(statePath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, json.Unmarshal(b, &st), test.ShouldBeNil)
	_, ok := st.Devices["88213-Yamaha-0-Engine Gateway"]
	test.That(t, ok, test.ShouldBeTrue)
}

func TestLabelChangeKeepsFirstSeen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VIAM_MODULE_DATA", dir)

	// First sight: address claim only, labeled by manufacturer.
	claimOnly := map[string]interface{}{
		"60928-3": entry(60928, 3, "14 Oct 26 09:00 -0400", map[string]interface{}{
			"Unique Number": float64(1037921), "Manufacturer Code": "Garmin", "Device Function": "Display",
		}),
	}
	s := newPersisting(t, &filteringAllPgn{readings: claimOnly}, nil)
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, device(t, got, "Garmin [1037921]")["first_seen"], test.ShouldEqual, "14 Oct 26 09:00 -0400")

	// Product info arrives: the label changes, the record does not restart.
	withProduct := map[string]interface{}{
		"60928-3":  claimOnly["60928-3"],
		"126996-3": entry(126996, 3, "14 Oct 26 09:30 -0400", map[string]interface{}{"Model ID": "GPSMAP 8612"}),
	}
	s = newPersisting(t, &filteringAllPgn{readings: withProduct}, nil)
	got, err = s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(got), test.ShouldEqual, 1)
	dev := device(t, got, "Garmin GPSMAP 8612 [1037921]")
	test.That(t, dev["first_seen"], test.ShouldEqual, "14 Oct 26 09:00 -0400")
	test.That(t, dev["last_seen"], test.ShouldEqual, "14 Oct 26 09:30 -0400")
}

func TestClaimlessSourceIsNotPersisted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VIAM_MODULE_DATA", dir)

	s := newPersisting(t, &filteringAllPgn{readings: map[string]interface{}{
		"127508-7-0": entry(127508, 7, "14 Oct 26 08:52 -0400", map[string]interface{}{
			"Instance": float64(0), "description": "Battery Status",
		}),
	}}, nil)
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, device(t, got, "src-7")["first_seen"], test.ShouldEqual, "14 Oct 26 08:52 -0400")

	// Reported, but a bus address is not an identity: nothing written.
	_, err = os.Stat(filepath.Join(dir, "devicelist-devices.json"))
	test.That(t, os.IsNotExist(err), test.ShouldBeTrue)
}

func TestPersistOffWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VIAM_MODULE_DATA", dir)

	s := newPersisting(t, &filteringAllPgn{readings: twinEngineReadings()}, &Config{Persist: boolPtr(false)})
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, device(t, got, "Yamaha CL7 Gateway [88213]")["first_seen"], test.ShouldEqual, "14 Oct 26 07:00 -0400")
	entries, err := os.ReadDir(dir)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(entries), test.ShouldEqual, 0)
}

func TestForgetAndReset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VIAM_MODULE_DATA", dir)
	statePath := filepath.Join(dir, "devicelist-devices.json")

	s := newPersisting(t, &filteringAllPgn{readings: twinEngineReadings()}, nil)
	_, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	// Unplugged: the bus is empty, the gateway is still remembered.
	s = newPersisting(t, &filteringAllPgn{readings: map[string]interface{}{}}, nil)
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(got), test.ShouldEqual, 1)

	status, err := s.DoCommand(context.Background(), map[string]interface{}{"command": "status"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, status["remembered_devices"], test.ShouldEqual, 1)
	test.That(t, status["persist_path"], test.ShouldEqual, statePath)

	// Forget by reading key.
	res, err := s.DoCommand(context.Background(), map[string]interface{}{"command": "forget", "device": "Yamaha CL7 Gateway [88213]"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, res["forgotten"], test.ShouldEqual, "88213-Yamaha-0-Engine Gateway")
	got, err = s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(got), test.ShouldEqual, 0)

	// Unknown key is an error, not a silent no-op.
	_, err = s.DoCommand(context.Background(), map[string]interface{}{"command": "forget", "device": "nope"})
	test.That(t, err, test.ShouldNotBeNil)

	// Reset removes the file; a restart starts empty.
	_, err = s.Readings(context.Background(), nil) // nothing live, nothing to save
	test.That(t, err, test.ShouldBeNil)
	s = newPersisting(t, &filteringAllPgn{readings: twinEngineReadings()}, nil)
	_, err = s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	res, err = s.DoCommand(context.Background(), map[string]interface{}{"command": "reset"})
	test.That(t, err, test.ShouldBeNil)
	test.That(t, res["reset"], test.ShouldEqual, true)
	_, err = os.Stat(statePath)
	test.That(t, os.IsNotExist(err), test.ShouldBeTrue)
	s = newPersisting(t, &filteringAllPgn{readings: map[string]interface{}{}}, nil)
	got, err = s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(got), test.ShouldEqual, 0)
}
