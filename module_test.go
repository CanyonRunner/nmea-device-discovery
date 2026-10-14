package nmeadevicediscovery

import (
	"context"
	"strings"
	"testing"

	"go.viam.com/rdk/components/sensor"
	"go.viam.com/test"
)

// fakeAllPgn stands in for the viamboat boat-all-pgn dependency, returning a
// canned history map shaped the way readings arrive after a gRPC hop: numbers
// as float64, timestamps as RFC822Z strings, keys "pgn-src".
type fakeAllPgn struct {
	sensor.Sensor
	readings map[string]interface{}
	gotExtra map[string]interface{}
}

func (f *fakeAllPgn) Readings(ctx context.Context, extra map[string]interface{}) (map[string]interface{}, error) {
	f.gotExtra = extra
	return f.readings, nil
}

func entry(pgn, src float64, ts string, fields map[string]interface{}) map[string]interface{} {
	m := map[string]interface{}{"pgn": pgn, "src": src, "timestamp": ts, "description": "x"}
	for k, v := range fields {
		m[k] = v
	}
	return m
}

func TestReadingsMergesIdentityPGNsPerDevice(t *testing.T) {
	fake := &fakeAllPgn{readings: map[string]interface{}{
		"60928-42": entry(60928, 42, "29 Jul 26 14:02 +0000", map[string]interface{}{
			"Unique Number": float64(12345), "Manufacturer Code": float64(999),
			"Device Function": float64(130), "Device Class": "Navigation",
		}),
		"126996-42": entry(126996, 42, "29 Jul 26 14:05 +0000", map[string]interface{}{
			"Model ID": "CR Box", "Product Code": float64(1234), "NMEA 2000 Version": 2.1,
		}),
		"126998-42": entry(126998, 42, "29 Jul 26 14:01 +0000", map[string]interface{}{
			"Installation Description #1": "helm",
		}),
	}}

	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake, cfg: &Config{TrackInstances: boolPtr(false)}}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	// With instance tracking off, the filter is pushed down to the all-pgn sensor.
	test.That(t, fake.gotExtra["pgns"], test.ShouldResemble,
		[]interface{}{pgnAddressClaim, pgnProductInfo, pgnConfigInfo})

	// One device, labeled "<mfg> <model> [<unique number>]".
	test.That(t, len(got), test.ShouldEqual, 1)
	dev, ok := got["999 CR Box [12345]"].(map[string]interface{})
	test.That(t, ok, test.ShouldBeTrue)

	// Fields merged from all three PGNs, renamed to snake_case.
	test.That(t, dev["src"], test.ShouldEqual, 42)
	test.That(t, dev["unique_number"], test.ShouldEqual, float64(12345))
	test.That(t, dev["device_class"], test.ShouldEqual, "Navigation")
	test.That(t, dev["model_id"], test.ShouldEqual, "CR Box")
	test.That(t, dev["installation_description_1"], test.ShouldEqual, "helm")

	// last_seen is the newest of the three entries.
	test.That(t, dev["last_seen"], test.ShouldEqual, "29 Jul 26 14:05 +0000")
}

func TestReadingsDeduplicatesMovedDevice(t *testing.T) {
	// The same physical device (identity 12345-999) claimed address 42, then
	// moved to 43 after losing contention; the all-pgn history keeps both.
	fake := &fakeAllPgn{readings: map[string]interface{}{
		"60928-42": entry(60928, 42, "29 Jul 26 14:00 +0000", map[string]interface{}{
			"Unique Number": float64(12345), "Manufacturer Code": float64(999),
		}),
		"60928-43": entry(60928, 43, "29 Jul 26 14:10 +0000", map[string]interface{}{
			"Unique Number": float64(12345), "Manufacturer Code": float64(999),
		}),
	}}

	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	// One device, at its most recent address. No product info seen, so the
	// label is the manufacturer alone.
	test.That(t, len(got), test.ShouldEqual, 1)
	dev := got["999 [12345]"].(map[string]interface{})
	test.That(t, dev["src"], test.ShouldEqual, 43)
	test.That(t, dev["last_seen"], test.ShouldEqual, "29 Jul 26 14:10 +0000")
}

func TestReadingsKeyFormatsLargeUniqueNumber(t *testing.T) {
	// Unique numbers arrive as float64 over gRPC; values >= 1e6 render in
	// scientific notation under %v ("1.037921e+06-Garmin" seen on a real
	// Garmin device). Keys must normalize to integer form. Known manufacturer
	// codes decode to their lookup name and stay strings.
	fake := &fakeAllPgn{readings: map[string]interface{}{
		"60928-3": entry(60928, 3, "07 Aug 26 09:00 +0000", map[string]interface{}{
			"Unique Number": float64(1037921), "Manufacturer Code": "Garmin",
		}),
	}}

	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	for k := range got {
		test.That(t, strings.Contains(k, "e+"), test.ShouldBeFalse)
	}
	dev, ok := got["Garmin [1037921]"].(map[string]interface{})
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, dev["src"], test.ShouldEqual, 3)
}

func TestReadingsKeySanitizesModelStrings(t *testing.T) {
	// Real-network values: a Furuno MFD reports a NUL-terminated fixed-length
	// model_id with junk after the terminator, and Simrad pads with doubled
	// spaces. Keys must stay clean; the model_id FIELD keeps the raw value.
	fake := &fakeAllPgn{readings: map[string]interface{}{
		"60928-0": entry(60928, 0, "07 Aug 26 16:55 -0400", map[string]interface{}{
			"Unique Number": float64(51359), "Manufacturer Code": "Furuno",
		}),
		"126996-0": entry(126996, 0, "07 Aug 26 16:55 -0400", map[string]interface{}{
			"Model ID": "TZT14\x004 TZT",
		}),
		"60928-2": entry(60928, 2, "07 Aug 26 16:55 -0400", map[string]interface{}{
			"Unique Number": float64(1589637), "Manufacturer Code": "Simrad",
		}),
		"126996-2": entry(126996, 2, "07 Aug 26 16:55 -0400", map[string]interface{}{
			"Model ID": "NSS4-12  iGPS",
		}),
	}}

	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	furuno, ok := got["Furuno TZT14 [51359]"].(map[string]interface{})
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, furuno["model_id"], test.ShouldEqual, "TZT14\x004 TZT")

	_, ok = got["Simrad NSS4-12 iGPS [1589637]"]
	test.That(t, ok, test.ShouldBeTrue)
}

func TestReadingsDeviceWithoutClaimKeyedByAddress(t *testing.T) {
	// Product info overheard from a device whose address claim we never saw.
	fake := &fakeAllPgn{readings: map[string]interface{}{
		"126996-7": entry(126996, 7, "29 Jul 26 14:00 +0000", map[string]interface{}{
			"Model ID": "Mystery Box",
		}),
	}}

	s := &nmeaDeviceDiscoveryDevicelist{allPgn: fake}
	got, err := s.Readings(context.Background(), nil)
	test.That(t, err, test.ShouldBeNil)

	// No address claim seen: the source address stands in for identity.
	dev, ok := got["Mystery Box [src-7]"].(map[string]interface{})
	test.That(t, ok, test.ShouldBeTrue)
	test.That(t, dev["model_id"], test.ShouldEqual, "Mystery Box")
}
