package mihomo

import (
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/model"
	"testing"
)

func TestMihomoProviderReferenceValidation(t *testing.T) {
	good := func() model.MihomoOptions {
		return model.MihomoOptions{ProviderReference: &model.MihomoProviderReference{URL: "https://publisher.invalid/members.json", Providers: []string{"edge-local"}, MaxAgeSeconds: 3600}}
	}
	for _, change := range []func(*model.MihomoOptions){
		func(o *model.MihomoOptions) { o.ProviderReference.URL = "http://publisher.invalid/members.json" },
		func(o *model.MihomoOptions) {
			o.ProviderReference.URL = "https://user:pass@publisher.invalid/members.json"
		},
		func(o *model.MihomoOptions) {
			o.ProviderReference.URL = "https://publisher.invalid/members.json?token=secret"
		},
		func(o *model.MihomoOptions) { o.ProviderReference.URL = "https://publisher.invalid/members.json?" },
		func(o *model.MihomoOptions) {
			o.ProviderReference.URL = "https://publisher.invalid/members.json#secret"
		},
		func(o *model.MihomoOptions) { o.ProviderReference.URL = "file:///tmp/members.json" },
		func(o *model.MihomoOptions) {
			negative := -1
			o.ProviderReference.SettleSeconds = &negative
		},
		func(o *model.MihomoOptions) {
			tooLong := 3601
			o.ProviderReference.SettleSeconds = &tooLong
		},
		func(o *model.MihomoOptions) {
			asOldAsThePublication := 3600
			o.ProviderReference.SettleSeconds = &asOldAsThePublication
		},
		func(o *model.MihomoOptions) {
			o.ProviderReference.MaxAgeSeconds = 60
			olderThanThePublication := 120
			o.ProviderReference.SettleSeconds = &olderThanThePublication
		},
		func(o *model.MihomoOptions) { o.ProviderReference.MaxAgeSeconds = 0 },
		func(o *model.MihomoOptions) { o.ProviderReference.MaxAgeSeconds = 604801 },
		func(o *model.MihomoOptions) { o.ProviderReference.Providers = nil },
		func(o *model.MihomoOptions) { o.ProviderReference.Providers = []string{"edge-local", "edge-local"} },
		func(o *model.MihomoOptions) { o.ProviderReference.Providers = []string{"bad\x1bname"} },
		func(o *model.MihomoOptions) { o.ExpectedProviders = map[string][]string{"edge-local": {"old"}} },
		func(o *model.MihomoOptions) {
			o.ExpectedProviders = map[string][]string{}
			for i := 0; i < 16; i++ {
				o.ExpectedProviders[fmt.Sprint(i)] = []string{"node"}
			}
		},
	} {
		o := good()
		change(&o)
		if Validate(o) == nil {
			t.Fatal("invalid reference accepted", o)
		}
	}
	for _, settle := range []int{0, 1, 120, 3599} {
		o := good()
		o.ProviderReference.SettleSeconds = &settle
		if err := Validate(o); err != nil {
			t.Fatal(settle, err)
		}
	}
	if err := Validate(good()); err != nil {
		t.Fatal("unset settle_seconds must be valid", err)
	}
	for _, url := range []string{"https://publisher.invalid/members.json", "http://127.0.0.1:8080/members.json", "http://[::1]:8080/members.json"} {
		o := good()
		o.ProviderReference.URL = url
		if err := Validate(o); err != nil {
			t.Fatal(err)
		}
	}
	ref := good().ProviderReference
	ref.URL = "http://127.0.0.1:8080/members.json"
	if ValidateReferenceTransport(ref, true) == nil {
		t.Fatal("proxied loopback accepted as trusted plaintext")
	}
}
