package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/devices"
	"github.com/fadymondy/hosbah/internal/pgtext"
	"github.com/fadymondy/hosbah/internal/push"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// The phone and desktop apps (docs/apps-plan.md): an app signs in like the web, then registers
// itself here for a long-lived refresh token (POST /api/auth/device/refresh, internal/account) and
// a push token. Everything is the caller's own; no team is involved.

// maxDevices caps one account's signed-in apps (a loop re-registering on every launch stops here).
const maxDevices = 50

type DeviceOut struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Platform   string    `json:"platform" enum:"ios,android,windows,macos,linux"`
	App        string    `json:"app" enum:"mobile,desktop"`
	Locale     string    `json:"locale" enum:"ar,en"`
	AppVersion string    `json:"app_version"`
	Push       bool      `json:"push" doc:"Registered for push notifications."`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

func deviceOut(d db.Device) DeviceOut {
	return DeviceOut{ID: d.ID, Name: d.Name, Platform: d.Platform, App: d.App, Locale: d.Locale,
		AppVersion: d.AppVersion, Push: d.PushToken != nil, CreatedAt: d.CreatedAt, LastSeenAt: d.LastSeenAt}
}

type DeviceListOut struct {
	Body struct {
		Devices []DeviceOut `json:"devices"`
	}
}

type DeviceCreateIn struct {
	Body struct {
		Name       string `json:"name" minLength:"1" maxLength:"80" doc:"Shown under Account, Devices (e.g. the phone's own name)."`
		Platform   string `json:"platform" enum:"ios,android,windows,macos,linux"`
		App        string `json:"app" enum:"mobile,desktop"`
		Locale     string `json:"locale,omitempty" enum:"ar,en"`
		AppVersion string `json:"app_version,omitempty" maxLength:"40"`
	}
}

type DeviceCreateOut struct {
	Body struct {
		Device DeviceOut `json:"device"`
		// RefreshToken is shown once. Keep it in the platform's secure storage.
		RefreshToken string `json:"refresh_token" doc:"hbr_... : trade it at POST /api/auth/device/refresh for a new session. Shown once."`
	}
}

type DevicePath struct {
	Device string `path:"device" maxLength:"64"`
}

type DevicePushIn struct {
	DevicePath
	Body struct {
		Token      string `json:"token" maxLength:"4096" doc:"The FCM registration token; empty turns push off for this device."`
		Locale     string `json:"locale,omitempty" enum:"ar,en"`
		AppVersion string `json:"app_version,omitempty" maxLength:"40"`
	}
}

type DeviceTestOut struct {
	Body struct {
		Sent int `json:"sent"`
	}
}

type NotificationPrefsOut struct {
	Body struct {
		// Events maps every alert the apps can push to whether this person gets it.
		Events map[string]bool `json:"events"`
		// PushAvailable is false when the server has no push credentials: settings still save,
		// and the desktop app still alerts from the live stream.
		PushAvailable bool `json:"push_available"`
	}
}

type NotificationPrefsIn struct {
	Body struct {
		Events map[string]bool `json:"events" doc:"Event to on/off. Events left out keep their current setting."`
	}
}

func (d *deps) registerDevices(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-my-devices", Method: http.MethodGet, Path: "/api/me/devices",
		Summary: "The phone and desktop apps signed in to my account", Tags: []string{"Account"},
	}, func(ctx context.Context, _ *struct{}) (*DeviceListOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		out := &DeviceListOut{}
		out.Body.Devices = []DeviceOut{}
		err = d.tx(ctx, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
			rows, err := q.ListDevicesOfUser(ctx, id.ID)
			for _, r := range rows {
				out.Body.Devices = append(out.Body.Devices, deviceOut(r))
			}
			return err
		})
		return out, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-my-device", Method: http.MethodPost, Path: "/api/me/devices", DefaultStatus: http.StatusCreated,
		Summary:     "Register this phone or desktop app (returns its refresh token, once)",
		Description: "Called by the Hosbah apps right after signing in. The refresh token keeps the app signed in for 90 days of inactivity; removing the device signs it out.",
		Tags:        []string{"Account"},
	}, func(ctx context.Context, in *DeviceCreateIn) (*DeviceCreateOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		locale := in.Body.Locale
		if locale == "" {
			locale = "ar"
		}
		token, hash, err := devices.NewRefresh()
		if err != nil {
			return nil, huma.Error500InternalServerError("could not create a token")
		}
		out := &DeviceCreateOut{}
		err = d.tx(ctx, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
			existing, err := q.ListDevicesOfUser(ctx, id.ID)
			if err != nil {
				return err
			}
			if len(existing) >= maxDevices {
				return huma.Error409Conflict("too many signed-in devices: remove one under Account, Devices first")
			}
			row, err := q.InsertDevice(ctx, db.InsertDeviceParams{
				UserID: id.ID, Name: strings.TrimSpace(in.Body.Name), Platform: in.Body.Platform, App: in.Body.App,
				Locale: locale, AppVersion: strings.TrimSpace(in.Body.AppVersion), RefreshHash: hash,
			})
			if err != nil {
				return err
			}
			out.Body.Device = deviceOut(row)
			return nil
		})
		if err != nil {
			return nil, err
		}
		out.Body.RefreshToken = token
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-my-device", Method: http.MethodDelete, Path: "/api/me/devices/{device}", DefaultStatus: http.StatusNoContent,
		Summary: "Sign a device out (its refresh and push tokens stop working)", Tags: []string{"Account"},
	}, func(ctx context.Context, in *DevicePath) (*struct{}, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		return nil, d.tx(ctx, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
			n, err := q.DeleteDeviceOfUser(ctx, db.DeleteDeviceOfUserParams{ID: in.Device, UserID: id.ID})
			if err == nil && n == 0 {
				return huma.Error404NotFound("device not found")
			}
			return err
		})
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-my-device-push", Method: http.MethodPut, Path: "/api/me/devices/{device}/push", DefaultStatus: http.StatusNoContent,
		Summary: "Register (or clear) this device's push token", Tags: []string{"Account"},
	}, func(ctx context.Context, in *DevicePushIn) (*struct{}, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		var token *string
		if t := strings.TrimSpace(in.Body.Token); t != "" {
			token = &t
		}
		if err := d.ownDevice(ctx, id.ID, in.Device); err != nil {
			return nil, err
		}
		if token != nil {
			// The same install may have been registered under another sign-in (an account
			// switch, possibly another person's): the token follows the install, so it leaves
			// the old row first. Only that row's users can read it, hence system scope.
			if err := d.tx(ctx, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
				return q.ClearPushTokenElsewhere(ctx, db.ClearPushTokenElsewhereParams{PushToken: token, ID: in.Device})
			}); err != nil {
				return nil, err
			}
		}
		return nil, d.tx(ctx, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
			_, err := q.SetDevicePushToken(ctx, db.SetDevicePushTokenParams{
				PushToken: token, Locale: optionalString(in.Body.Locale), AppVersion: optionalString(strings.TrimSpace(in.Body.AppVersion)),
				ID: in.Device, UserID: id.ID,
			})
			return err
		})
	})

	huma.Register(api, huma.Operation{
		OperationID: "test-my-device-push", Method: http.MethodPost, Path: "/api/me/devices/{device}/test",
		Summary: "Send a test notification to one of my devices", Tags: []string{"Account"},
	}, func(ctx context.Context, in *DevicePath) (*DeviceTestOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		if err := d.ownDevice(ctx, id.ID, in.Device); err != nil {
			return nil, err
		}
		err = push.Current().SendTest(ctx, id.ID, in.Device)
		switch {
		case err == nil:
			out := &DeviceTestOut{}
			out.Body.Sent = 1
			return out, nil
		case errors.Is(err, push.ErrDisabled):
			return nil, huma.Error503ServiceUnavailable(err.Error())
		case errors.Is(err, push.ErrNoToken):
			return nil, huma.Error409Conflict(err.Error())
		default:
			return nil, huma.Error502BadGateway("the push service did not take the message")
		}
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-my-notification-prefs", Method: http.MethodGet, Path: "/api/me/notification-prefs",
		Summary: "Which alerts are pushed to my phones and desktops", Tags: []string{"Account"},
	}, func(ctx context.Context, _ *struct{}) (*NotificationPrefsOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		var muted []string
		err = d.tx(ctx, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
			p, err := q.NotificationPrefsOf(ctx, id.ID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			muted = p.MutedEvents
			return err
		})
		if err != nil {
			return nil, err
		}
		return prefsOut(muted), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-my-notification-prefs", Method: http.MethodPut, Path: "/api/me/notification-prefs",
		Summary: "Choose which alerts are pushed to my phones and desktops", Tags: []string{"Account"},
	}, func(ctx context.Context, in *NotificationPrefsIn) (*NotificationPrefsOut, error) {
		id, err := current(ctx)
		if err != nil {
			return nil, err
		}
		for e := range in.Body.Events {
			if !push.KnownEvent(e) {
				return nil, huma.Error422UnprocessableEntity("unknown event " + e)
			}
		}
		muted := pgtext.Array{}
		err = d.tx(ctx, tenancy.Scope{UserID: id.ID}, func(q *db.Queries, _ *sql.Tx) error {
			cur, err := q.NotificationPrefsOf(ctx, id.ID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			for _, e := range push.Events {
				on, given := in.Body.Events[e]
				if (given && !on) || (!given && slices.Contains(cur.MutedEvents, e)) {
					muted = append(muted, e)
				}
			}
			_, err = q.UpsertNotificationPrefs(ctx, db.UpsertNotificationPrefsParams{UserID: id.ID, MutedEvents: muted})
			return err
		})
		if err != nil {
			return nil, err
		}
		return prefsOut(muted), nil
	})
}

func prefsOut(muted []string) *NotificationPrefsOut {
	out := &NotificationPrefsOut{}
	out.Body.Events = map[string]bool{}
	for _, e := range push.Events {
		out.Body.Events[e] = !slices.Contains(muted, e)
	}
	out.Body.PushAvailable = push.Current().Enabled()
	return out
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ownDevice is a 404 unless deviceID is one of the user's own devices.
func (d *deps) ownDevice(ctx context.Context, userID, deviceID string) error {
	owned := false
	if err := d.tx(ctx, tenancy.Scope{UserID: userID}, func(q *db.Queries, _ *sql.Tx) error {
		rows, err := q.ListDevicesOfUser(ctx, userID)
		owned = slices.ContainsFunc(rows, func(r db.Device) bool { return r.ID == deviceID })
		return err
	}); err != nil {
		return err
	}
	if !owned {
		return huma.Error404NotFound("device not found")
	}
	return nil
}
