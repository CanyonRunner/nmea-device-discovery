package nmeadevicediscovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	sensor "go.viam.com/rdk/components/sensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

var (
	Devicelist       = resource.NewModel("viam-soleng", "nmea-device-discovery", "devicelist")
	errUnimplemented = errors.New("unimplemented")
)

func init() {
	resource.RegisterComponent(sensor.API, Devicelist,
		resource.Registration[sensor.Sensor, *Config]{
			Constructor: newNmeaDeviceDiscoveryDevicelist,
		},
	)
}

type Config struct {
	// AllPGNComponent names the viamboat boat-all-pgn sensor component whose
	// accumulated bus history this model reshapes into a device inventory.
	AllPGNComponent string `json:"all-pgn-component"`
	// TrackInstances widens the read to the whole all-pgn history and adds an
	// "instances" list to each device: one entry per (PGN, instance) the
	// device transmits, so a gateway reporting two engines or three tanks is
	// broken out per engine or tank. On by default; set it to false to push
	// the identity-PGN filter down to all-pgn and omit the list.
	TrackInstances *bool `json:"track-instances,omitempty"`
}

// Validate ensures all parts of the config are valid and important fields exist.
// Returns three values:
//  1. Required dependencies: other resources that must exist for this resource to work.
//  2. Optional dependencies: other resources that may exist but are not required.
//  3. An error if any Config fields are missing or invalid.
func (cfg *Config) Validate(path string) ([]string, []string, error) {
	if cfg.AllPGNComponent == "" {
		return nil, nil, resource.NewConfigValidationFieldRequiredError(path, "all-pgn-component")
	}
	return []string{cfg.AllPGNComponent}, nil, nil
}

type nmeaDeviceDiscoveryDevicelist struct {
	resource.AlwaysRebuild
	resource.Named

	name resource.Name

	logger logging.Logger
	cfg    *Config

	// allPgn is the viamboat boat-all-pgn sensor this model reads through. It
	// holds the bus history (latest message per PGN and source, with
	// timestamps); this model keeps no state of its own.
	allPgn sensor.Sensor

	cancelCtx  context.Context
	cancelFunc func()
}

func newNmeaDeviceDiscoveryDevicelist(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (sensor.Sensor, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}

	return NewDevicelist(ctx, deps, rawConf.ResourceName(), conf, logger)

}

func NewDevicelist(ctx context.Context, deps resource.Dependencies, name resource.Name, conf *Config, logger logging.Logger) (sensor.Sensor, error) {
	allPgn, err := sensor.FromProvider(deps, conf.AllPGNComponent)
	if err != nil {
		return nil, fmt.Errorf("all-pgn-component %q not found in dependencies: %w", conf.AllPGNComponent, err)
	}

	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	s := &nmeaDeviceDiscoveryDevicelist{
		name:       name,
		logger:     logger,
		cfg:        conf,
		allPgn:     allPgn,
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
	}
	return s, nil
}

func (s *nmeaDeviceDiscoveryDevicelist) Name() resource.Name {
	return s.name
}

// The NMEA 2000 device-identity PGNs this model reshapes into an inventory.
const (
	pgnAddressClaim = 60928  // ISO Address Claim: NAME fields, source address
	pgnProductInfo  = 126996 // Product Information
	pgnConfigInfo   = 126998 // Configuration Information
)

// identityFieldNames maps the gonmea field names of each identity PGN to the
// snake_case keys this sensor reports.
var identityFieldNames = map[int]map[string]string{
	pgnAddressClaim: {
		"Unique Number":         "unique_number",
		"Manufacturer Code":     "manufacturer_code",
		"Device Instance Lower": "device_instance_lower",
		"Device Instance Upper": "device_instance_upper",
		"Device Function":       "device_function",
		"Device Class":          "device_class",
		"System Instance":       "system_instance",
		"Industry Group":        "industry_group",
	},
	pgnProductInfo: {
		"NMEA 2000 Version":     "nmea2000_version",
		"Product Code":          "product_code",
		"Model ID":              "model_id",
		"Software Version Code": "software_version_code",
		"Model Version":         "model_version",
		"Model Serial Code":     "model_serial_code",
		"Certification Level":   "certification_level",
		"Load Equivalency":      "load_equivalency",
	},
	pgnConfigInfo: {
		"Installation Description #1": "installation_description_1",
		"Installation Description #2": "installation_description_2",
		"Manufacturer Information":    "manufacturer_information",
	},
}

// asInt tolerates the numeric shapes a reading can arrive in: native ints from
// an in-process call, float64 once values have crossed a gRPC/structpb hop.
func asInt(v interface{}) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	}
	return 0, false
}

// trackInstances reports the effective setting: true unless the config sets
// "track-instances": false. Nil-safe so tests can build the sensor without a
// Config.
func (s *nmeaDeviceDiscoveryDevicelist) trackInstances() bool {
	return s.cfg == nil || s.cfg.TrackInstances == nil || *s.cfg.TrackInstances
}

// instanceSeen is one (PGN, instance) pair a source address transmits, e.g.
// Engine Parameters for engine 1 or Fluid Level for tank 2.
type instanceSeen struct {
	pgn, instance int
	description   string
	lastSeen      time.Time
	lastSeenRaw   string
}

// instanceOf extracts the instance a non-identity entry belongs to. viamboat
// keys instance-bearing messages "<pgn>-<src>-<instance>"; the decoded
// payload also carries the field as "Instance". Entries with neither are not
// instanced (position, heading, ...) and are not reported.
func instanceOf(key string, entry map[string]interface{}) (int, bool) {
	if parts := strings.Split(key, "-"); len(parts) == 3 {
		if n, err := strconv.Atoi(parts[2]); err == nil {
			return n, true
		}
	}
	return asInt(entry["Instance"])
}

// parseTimestamp reads the all-pgn timestamp (CANTimeFormat.String(), RFC822Z).
func parseTimestamp(entry map[string]interface{}) (time.Time, string, bool) {
	raw, ok := entry["timestamp"].(string)
	if !ok {
		return time.Time{}, "", false
	}
	ts, err := time.Parse(time.RFC822Z, raw)
	return ts, raw, err == nil
}

// Readings returns one entry per device seen on the NMEA 2000 network, built
// by filtering the all-pgn sensor's accumulated history down to the identity
// PGNs and merging them per device. Devices are identified by their ISO NAME
// from the address claim (unique number, manufacturer, device instance and
// function; see stableIdentity) rather than by source address: addresses
// move under contention, and keying by address would report ghost devices
// at stale addresses. Entries are never pruned; last_seen lets the caller
// decide what is stale.
func (s *nmeaDeviceDiscoveryDevicelist) Readings(ctx context.Context, extra map[string]interface{}) (map[string]interface{}, error) {
	// By default the filter is pushed down to the all-pgn sensor so only the
	// identity PGNs cross the hop. Instance tracking needs the whole history:
	// any PGN may carry an instance, so no fixed list would stay correct.
	var filter map[string]interface{}
	if !s.trackInstances() {
		filter = map[string]interface{}{
			"pgns": []interface{}{pgnAddressClaim, pgnProductInfo, pgnConfigInfo},
		}
	}
	raw, err := s.allPgn.Readings(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("Reading from all-pgn-component: %w", err)
	}

	// The all-pgn history holds one entry per (PGN, source[, instance]); group
	// the up to three identity entries of each source address together, and
	// when tracking instances, the (PGN, instance) pairs it transmits.
	perSrc := map[int]map[int]map[string]interface{}{}
	instancesPerSrc := map[int]map[string]instanceSeen{} // keyed "<pgn>/<instance>"
	for key, v := range raw {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		pgn, okP := asInt(entry["pgn"])
		src, okS := asInt(entry["src"])
		if !okP || !okS {
			continue
		}
		if _, isIdentity := identityFieldNames[pgn]; isIdentity {
			if perSrc[src] == nil {
				perSrc[src] = map[int]map[string]interface{}{}
			}
			perSrc[src][pgn] = entry
			continue
		}
		if !s.trackInstances() {
			continue
		}
		instance, ok := instanceOf(key, entry)
		if !ok {
			continue
		}
		seen := instanceSeen{pgn: pgn, instance: instance}
		seen.description, _ = entry["description"].(string)
		seen.lastSeen, seen.lastSeenRaw, _ = parseTimestamp(entry)
		ik := fmt.Sprintf("%d/%d", pgn, instance)
		if instancesPerSrc[src] == nil {
			instancesPerSrc[src] = map[string]instanceSeen{}
		}
		if prev, dup := instancesPerSrc[src][ik]; !dup || seen.lastSeen.After(prev.lastSeen) {
			instancesPerSrc[src][ik] = seen
		}
		// A source we only know from instanced data still counts as a device
		// (keyed by address until its claim is seen).
		if perSrc[src] == nil {
			perSrc[src] = map[int]map[string]interface{}{}
		}
	}

	// Merge each source's entries into one device object and de-duplicate by
	// stable identity: a device that moved addresses (contention, commanded
	// address) appears under several sources, and only its most recent
	// incarnation is kept.
	winners := map[string]mergedDevice{} // keyed by stable identity
	for src, entries := range perSrc {
		device := map[string]interface{}{"src": src}
		var lastSeen time.Time
		lastSeenRaw := ""
		instanceCount := 0
		for pgn, entry := range entries {
			for from, to := range identityFieldNames[pgn] {
				if val, has := entry[from]; has {
					device[to] = val
				}
			}
			// The all-pgn timestamp is CANTimeFormat.String() (RFC822Z);
			// last_seen is the newest across the merged entries.
			if ts, tsRaw, parsed := parseTimestamp(entry); parsed && ts.After(lastSeen) {
				lastSeen = ts
				lastSeenRaw = tsRaw
			} else if tsRaw != "" && !parsed && lastSeenRaw == "" {
				lastSeenRaw = tsRaw
			}
		}
		// Instanced data is sent far more often than identity PGNs, so it is
		// the better liveness signal: a gateway streaming engine data is on
		// the bus even if its last address claim is hours old.
		if s.trackInstances() {
			instances := make([]instanceSeen, 0, len(instancesPerSrc[src]))
			instanceCount = len(instancesPerSrc[src])
			for _, inst := range instancesPerSrc[src] {
				instances = append(instances, inst)
				if inst.lastSeen.After(lastSeen) {
					lastSeen = inst.lastSeen
					lastSeenRaw = inst.lastSeenRaw
				}
			}
			sort.Slice(instances, func(i, j int) bool {
				if instances[i].pgn != instances[j].pgn {
					return instances[i].pgn < instances[j].pgn
				}
				return instances[i].instance < instances[j].instance
			})
			rendered := make([]interface{}, 0, len(instances))
			for _, inst := range instances {
				m := map[string]interface{}{
					"pgn":      inst.pgn,
					"instance": inst.instance,
				}
				if inst.description != "" {
					m["description"] = inst.description
				}
				if inst.lastSeenRaw != "" {
					m["last_seen"] = inst.lastSeenRaw
				}
				rendered = append(rendered, m)
			}
			device["instances"] = rendered
		}
		if lastSeenRaw != "" {
			device["last_seen"] = lastSeenRaw
		}

		// Stable identity from the address claim; a device we have only seen
		// identity fragments from (no claim yet) is identified by its address.
		identity := fmt.Sprintf("src-%d", src)
		if _, has := entries[pgnAddressClaim]; has {
			identity = stableIdentity(device)
		}

		candidate := mergedDevice{fields: device, src: src, lastSeen: lastSeen, instanceCount: instanceCount}
		if prev, seen := winners[identity]; seen && !candidate.beats(prev) {
			continue
		}
		winners[identity] = candidate
	}

	// Render display keys from the winners: "<manufacturer> <model> [<unique>]"
	// reads like a device name in the Viam app, while the bracketed unique
	// number (or source address for claim-less devices) keeps keys unique when
	// a network carries several identical products.
	devices := map[string]interface{}{}
	for key, w := range disambiguate(winners) {
		devices[key] = w.fields
	}

	return devices, nil
}

// disambiguate renders display keys for the winners and resolves collisions:
// distinct devices can share manufacturer, model and unique number (the two
// halves of a dual gateway differ only by device instance; a plotter's
// virtual nodes differ by device function). A colliding group gets the
// shortest qualifier that tells its members apart: " #<device instance>",
// then also " (<device function>)", then also " (src <n>)".
func disambiguate(winners map[string]mergedDevice) map[string]mergedDevice {
	byKey := map[string][]mergedDevice{}
	for identity, w := range winners {
		k := deviceDisplayKey(identity, w.fields)
		byKey[k] = append(byKey[k], w)
	}
	qualifiers := []func(mergedDevice) string{
		func(w mergedDevice) string { return fmt.Sprintf(" #%d", deviceInstance(w.fields)) },
		func(w mergedDevice) string { return fmt.Sprintf(" (%v)", intIfNumeric(w.fields["device_function"])) },
		func(w mergedDevice) string { return fmt.Sprintf(" (src %d)", w.src) },
	}
	out := map[string]mergedDevice{}
	for base, group := range byKey {
		if len(group) == 1 {
			out[base] = group[0]
			continue
		}
		for level := 1; level <= len(qualifiers); level++ {
			keys := make([]string, len(group))
			unique := true
			seen := map[string]bool{}
			for i, w := range group {
				keys[i] = base
				for _, q := range qualifiers[:level] {
					keys[i] += q(w)
				}
				if seen[keys[i]] {
					unique = false
				}
				seen[keys[i]] = true
			}
			if unique || level == len(qualifiers) {
				for i, w := range group {
					out[keys[i]] = w
				}
				break
			}
		}
	}
	return out
}

// stableIdentity is the device's ISO NAME minus its address: unique number,
// manufacturer, device instance and device function. Unique number plus
// manufacturer alone is not unique on a real bus: Garmin MFDs expose several
// virtual nodes (Display, Button Interface) under one unique number, and an
// Actisense dual gateway claims two addresses that differ only by device
// instance. Both are live at once and must be reported separately.
func stableIdentity(device map[string]interface{}) string {
	return fmt.Sprintf("%v-%v-%d-%v",
		intIfNumeric(device["unique_number"]),
		intIfNumeric(device["manufacturer_code"]),
		deviceInstance(device),
		intIfNumeric(device["device_function"]))
}

// deviceInstance assembles the NMEA 2000 device instance from the two
// address-claim fields: lower (3 bits) and upper (5 bits).
func deviceInstance(device map[string]interface{}) int {
	lower, _ := asInt(device["device_instance_lower"])
	upper, _ := asInt(device["device_instance_upper"])
	return lower | upper<<3
}

// mergedDevice is one source address's merged identity entries, competing
// with the same device's other addresses for the single reported entry.
type mergedDevice struct {
	fields        map[string]interface{}
	src           int
	lastSeen      time.Time
	instanceCount int
}

// beats decides which address of a device is reported. Newest last_seen
// wins; the all-pgn timestamps have one-minute resolution, so ties are
// common and must not fall through to map iteration order. On a tie prefer
// the address with live instanced traffic, then the more completely
// described one, then the lower address.
func (m mergedDevice) beats(prev mergedDevice) bool {
	if !m.lastSeen.Equal(prev.lastSeen) {
		return m.lastSeen.After(prev.lastSeen)
	}
	if m.instanceCount != prev.instanceCount {
		return m.instanceCount > prev.instanceCount
	}
	if len(m.fields) != len(prev.fields) {
		return len(m.fields) > len(prev.fields)
	}
	return m.src < prev.src
}

// intIfNumeric normalizes a value to int when it is numeric. Values arrive as
// float64 after a structpb hop, and %v renders large floats in scientific
// notation (1.037921e+06); lookup-rendered strings ("Garmin") pass through.
func intIfNumeric(v interface{}) interface{} {
	if n, ok := asInt(v); ok {
		return n
	}
	return v
}

// sanitizeLabel makes a decoded NMEA string safe and tidy for use in a map
// key. Fixed-length string fields follow the C convention of NUL termination
// with junk after the terminator (a real Furuno MFD reports model_id
// "TZT14\x004 TZT" — the model is "TZT14"), and model IDs often carry doubled
// spaces; truncate at the first NUL, then collapse whitespace and controls.
func sanitizeLabel(s string) string {
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return r <= ' ' || r == 0x7F
	}), " ")
}

// deviceDisplayKey builds the human-facing map key for a merged device:
// "<manufacturer> <model> [<unique number>]", with graceful fallbacks when
// product info or the address claim has not been seen yet.
func deviceDisplayKey(identity string, device map[string]interface{}) string {
	label := ""
	if mfg, has := device["manufacturer_code"]; has {
		label = fmt.Sprintf("%v", intIfNumeric(mfg))
	}
	if model, ok := device["model_id"].(string); ok {
		if clean := sanitizeLabel(model); clean != "" {
			if label != "" {
				label += " "
			}
			label += clean
		}
	}

	suffix := identity // "src-N" for claim-less devices
	if unique, ok := asInt(device["unique_number"]); ok {
		suffix = fmt.Sprintf("%d", unique)
	}

	if label == "" {
		return suffix
	}
	return fmt.Sprintf("%s [%s]", label, suffix)
}

func (s *nmeaDeviceDiscoveryDevicelist) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *nmeaDeviceDiscoveryDevicelist) Status(ctx context.Context) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *nmeaDeviceDiscoveryDevicelist) Close(context.Context) error {
	// Put close code here
	s.cancelFunc()
	return nil
}
