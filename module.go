package nmeadevicediscovery

import (
	"context"
	"errors"
	"fmt"
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

// Readings returns one entry per device seen on the NMEA 2000 network, built
// by filtering the all-pgn sensor's accumulated history down to the identity
// PGNs and merging them per device. Entries are keyed by the device's stable
// identity (unique number + manufacturer code from its address claim) rather
// than its source address: addresses move under contention, and keying by
// address would report ghost devices at stale addresses. Entries are never
// pruned; last_seen lets the caller decide what is stale.
func (s *nmeaDeviceDiscoveryDevicelist) Readings(ctx context.Context, extra map[string]interface{}) (map[string]interface{}, error) {
	raw, err := s.allPgn.Readings(ctx, map[string]interface{}{
		"pgns": []interface{}{pgnAddressClaim, pgnProductInfo, pgnConfigInfo},
	})
	if err != nil {
		return nil, fmt.Errorf("Reading from all-pgn-component: %w", err)
	}

	// The all-pgn history holds one entry per (PGN, source); group the up to
	// three identity entries of each source address together.
	perSrc := map[int]map[int]map[string]interface{}{}
	for _, v := range raw {
		entry, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		pgn, okP := asInt(entry["pgn"])
		src, okS := asInt(entry["src"])
		if !okP || !okS {
			continue
		}
		if perSrc[src] == nil {
			perSrc[src] = map[int]map[string]interface{}{}
		}
		perSrc[src][pgn] = entry
	}

	devices := map[string]interface{}{}
	lastSeenByKey := map[string]time.Time{}
	for src, entries := range perSrc {
		device := map[string]interface{}{"src": src}
		var lastSeen time.Time
		lastSeenRaw := ""
		for pgn, entry := range entries {
			for from, to := range identityFieldNames[pgn] {
				if val, has := entry[from]; has {
					device[to] = val
				}
			}
			// The all-pgn timestamp is CANTimeFormat.String() (RFC822Z);
			// last_seen is the newest across the merged entries.
			if tsRaw, ok := entry["timestamp"].(string); ok {
				if ts, err := time.Parse(time.RFC822Z, tsRaw); err == nil && ts.After(lastSeen) {
					lastSeen = ts
					lastSeenRaw = tsRaw
				} else if err != nil && lastSeenRaw == "" {
					lastSeenRaw = tsRaw
				}
			}
		}
		if lastSeenRaw != "" {
			device["last_seen"] = lastSeenRaw
		}

		// Stable identity from the address claim; a device we have only seen
		// identity fragments from (no claim yet) is keyed by its address.
		key := fmt.Sprintf("src-%d", src)
		if claim, has := entries[pgnAddressClaim]; has {
			// Numeric values arrive as float64 after a structpb hop, and %v
			// renders large floats in scientific notation (1.037921e+06);
			// normalize to integers so keys stay readable and stable.
			unique := claim["Unique Number"]
			if n, ok := asInt(unique); ok {
				unique = n
			}
			mfg := claim["Manufacturer Code"] // stays a string for known makers ("Garmin")
			if n, ok := asInt(mfg); ok {
				mfg = n
			}
			key = fmt.Sprintf("%v-%v", unique, mfg)
		}

		// A device that moved addresses appears under several sources with the
		// same identity; keep only its most recent incarnation.
		if prev, seen := lastSeenByKey[key]; seen && !lastSeen.After(prev) {
			continue
		}
		lastSeenByKey[key] = lastSeen
		devices[key] = device
	}

	return devices, nil
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
