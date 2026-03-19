package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/device"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edbnme/pen/internal/format"
)

func registerEmulateTools(s *mcp.Server, deps *Deps) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "pen_emulate",
		Description: `Set device emulation: device presets, CPU throttling, network throttling. Settings persist until cleared.

Device presets: "iPhone 14", "iPhone 14 Pro", "iPhone 14 Pro Max", "iPhone 13", "iPhone 13 Pro", "iPhone 13 Pro Max", "iPhone 12", "iPhone 12 Pro", "iPhone SE", "iPad", "iPad Pro", "Pixel 5", "Pixel 7", "Galaxy S9", "Galaxy S8", "reset" (clears emulation).

Network presets: slow-3g, 3g, 4g, wifi, offline.
Custom network: set latencyMs, downloadKbps, uploadKbps for exact conditions.

You can combine device + CPU + network throttling in one call.`,
		Annotations: &mcp.ToolAnnotations{
			Title:           "Emulate Device",
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, makeEmulateHandler(deps))
}

// --- pen_emulate ---

type emulateInput struct {
	Device          string  `json:"device,omitempty"            jsonschema:"Device preset (e.g. 'iPhone 14', 'Pixel 7', 'iPad', 'reset')"`
	CPUThrottling   float64 `json:"cpuThrottling,omitempty"     jsonschema:"CPU slowdown factor (e.g. 4 = 4x slower)"`
	NetworkThrottle string  `json:"networkThrottling,omitempty" jsonschema:"Network preset: slow-3g, 3g, 4g, wifi, offline"`
	LatencyMs       int     `json:"latencyMs,omitempty"         jsonschema:"Custom network latency in milliseconds (overrides preset)"`
	DownloadKbps    float64 `json:"downloadKbps,omitempty"      jsonschema:"Custom download speed in Kbps (overrides preset)"`
	UploadKbps      float64 `json:"uploadKbps,omitempty"        jsonschema:"Custom upload speed in Kbps (overrides preset)"`
}

// devicePreset maps user-friendly names to chromedp device.Info or custom specs.
// For devices available in chromedp/device package, we use those directly.
// For newer devices (Pixel 7), we define specs from official device data.
var devicePresets = map[string]device.Info{
	// iPhones
	"iphone 14":         device.IPhone14.Device(),
	"iphone 14 pro":     device.IPhone14Pro.Device(),
	"iphone 14 pro max": device.IPhone14ProMax.Device(),
	"iphone 14 plus":    device.IPhone14Plus.Device(),
	"iphone 13":         device.IPhone13.Device(),
	"iphone 13 pro":     device.IPhone13Pro.Device(),
	"iphone 13 pro max": device.IPhone13ProMax.Device(),
	"iphone 13 mini":    device.IPhone13Mini.Device(),
	"iphone 12":         device.IPhone12.Device(),
	"iphone 12 pro":     device.IPhone12Pro.Device(),
	"iphone 12 pro max": device.IPhone12ProMax.Device(),
	"iphone 12 mini":    device.IPhone12Mini.Device(),
	"iphone se":         device.IPhoneSE.Device(),
	"iphone x":          device.IPhoneX.Device(),
	"iphone 11":         device.IPhone11.Device(),
	"iphone 11 pro":     device.IPhone11Pro.Device(),
	"iphone 11 pro max": device.IPhone11ProMax.Device(),

	// iPads
	"ipad":     device.IPad.Device(),
	"ipad pro": device.IPadPro.Device(),

	// Pixel (5 is in chromedp; 7 is custom)
	"pixel 5": device.Pixel5.Device(),
	"pixel 7": {
		Name:      "Pixel 7",
		UserAgent: "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Mobile Safari/537.36",
		Width:     412,
		Height:    915,
		Scale:     2.625,
		Mobile:    true,
		Touch:     true,
	},

	// Galaxy
	"galaxy s9": device.GalaxyS9.Device(),
	"galaxy s8": device.GalaxyS8.Device(),
}

func resolveDevicePreset(name string) (device.Info, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if d, ok := devicePresets[key]; ok {
		return d, nil
	}

	// List available presets for error message.
	available := make([]string, 0, len(devicePresets))
	for k := range devicePresets {
		available = append(available, k)
	}
	return device.Info{}, fmt.Errorf("unknown device %q — available presets: %s, or use 'reset' to clear emulation",
		name, strings.Join(available, ", "))
}

func makeEmulateHandler(deps *Deps) func(context.Context, *mcp.CallToolRequest, emulateInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input emulateInput) (*mcp.CallToolResult, any, error) {
		cdpCtx, err := deps.CDP.Context()
		if err != nil {
			return toolError("CDP not connected: " + err.Error())
		}

		var applied []string

		// Handle "reset" device — clears all emulation.
		if strings.EqualFold(strings.TrimSpace(input.Device), "reset") {
			if err := chromedp.Run(cdpCtx, chromedp.EmulateReset()); err != nil {
				return toolError("failed to reset emulation: " + err.Error())
			}
			applied = append(applied, "Device emulation reset to defaults")
		} else if input.Device != "" {
			// Resolve device preset.
			d, err := resolveDevicePreset(input.Device)
			if err != nil {
				return toolError(err.Error())
			}

			// Apply device emulation using the same CDP calls as chromedp.Emulate:
			// 1. SetUserAgentOverride
			// 2. SetDeviceMetricsOverride (viewport, scale, mobile flag, orientation)
			// 3. SetTouchEmulationEnabled
			var angle int64
			orientation := emulation.OrientationTypePortraitPrimary
			if d.Landscape {
				orientation, angle = emulation.OrientationTypeLandscapePrimary, 90
			}

			if err := chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				if err := emulation.SetUserAgentOverride(d.UserAgent).Do(ctx); err != nil {
					return fmt.Errorf("setUserAgentOverride: %w", err)
				}
				if err := emulation.SetDeviceMetricsOverride(d.Width, d.Height, d.Scale, d.Mobile).
					WithScreenOrientation(&emulation.ScreenOrientation{
						Type:  orientation,
						Angle: angle,
					}).Do(ctx); err != nil {
					return fmt.Errorf("setDeviceMetricsOverride: %w", err)
				}
				if err := emulation.SetTouchEmulationEnabled(d.Touch).Do(ctx); err != nil {
					return fmt.Errorf("setTouchEmulationEnabled: %w", err)
				}
				return nil
			})); err != nil {
				return toolError("device emulation failed: " + err.Error())
			}

			orient := "portrait"
			if d.Landscape {
				orient = "landscape"
			}
			applied = append(applied, fmt.Sprintf("Device: %s (%dx%d @%.1fx, %s, mobile=%v, touch=%v)",
				d.Name, d.Width, d.Height, d.Scale, orient, d.Mobile, d.Touch))
		}

		// CPU throttling via Emulation.setCPUThrottlingRate
		if input.CPUThrottling > 1 {
			if err := chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return emulation.SetCPUThrottlingRate(input.CPUThrottling).Do(ctx)
			})); err != nil {
				return toolError("CPU throttling failed: " + err.Error())
			}
			applied = append(applied, fmt.Sprintf("CPU throttling: %.0fx slowdown", input.CPUThrottling))
		}

		// Network throttling via Network.emulateNetworkConditions
		if input.NetworkThrottle != "" {
			latency, down, up, err := networkPreset(input.NetworkThrottle)
			if err != nil {
				return toolError(err.Error())
			}
			offline := strings.EqualFold(input.NetworkThrottle, "offline")
			if err := chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return network.EmulateNetworkConditions(offline, float64(latency), down, up).Do(ctx)
			})); err != nil {
				return toolError("network throttling failed: " + err.Error())
			}
			applied = append(applied, fmt.Sprintf("Network: %s (latency=%dms, down=%.1f Mbps, up=%.1f Mbps)",
				input.NetworkThrottle, latency, down*8/1_000_000, up*8/1_000_000))
		} else if input.LatencyMs > 0 || input.DownloadKbps > 0 || input.UploadKbps > 0 {
			// Custom network throttling values.
			latency := float64(input.LatencyMs)
			downBps := input.DownloadKbps * 1000 / 8 // Kbps → bytes/sec
			upBps := input.UploadKbps * 1000 / 8
			if err := chromedp.Run(cdpCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return network.EmulateNetworkConditions(false, latency, downBps, upBps).Do(ctx)
			})); err != nil {
				return toolError("custom network throttling failed: " + err.Error())
			}
			applied = append(applied, fmt.Sprintf("Network: custom (latency=%dms, down=%.0f Kbps, up=%.0f Kbps)",
				input.LatencyMs, input.DownloadKbps, input.UploadKbps))
		}

		if len(applied) == 0 {
			return toolError("no emulation parameters provided — specify device, cpuThrottling, or networkThrottling")
		}

		applied = append(applied, "NOTE: These settings persist until cleared (use device='reset') or browser restart. Performance metrics collected now will reflect emulated conditions.")

		output := format.ToolResult("Emulation Applied",
			format.BulletList(applied),
		)

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: output}},
		}, nil, nil
	}
}

// networkPreset returns (latencyMs, downloadBytesPerSec, uploadBytesPerSec).
// Values match Chrome DevTools standard presets.
func networkPreset(name string) (int, float64, float64, error) {
	switch strings.ToLower(name) {
	case "slow-3g":
		return 2000, 50_000, 50_000, nil // Slow 3G: 400 Kbps down, 400 Kbps up
	case "3g":
		return 563, 187_500, 93_750, nil // Fast 3G: 1.5 Mbps down, 750 Kbps up
	case "4g":
		return 170, 500_000, 375_000, nil // Regular 4G: 4 Mbps down, 3 Mbps up
	case "wifi":
		return 2, 3_750_000, 1_875_000, nil // WiFi: 30 Mbps down, 15 Mbps up
	case "offline":
		return 0, 0, 0, nil // Offline: no connectivity
	default:
		return 0, 0, 0, fmt.Errorf("unknown network preset %q (valid: slow-3g, 3g, 4g, wifi, offline)", name)
	}
}
