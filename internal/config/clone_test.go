package config_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/model"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestConfigCloneDetachesEveryMutableField(t *testing.T) {
	c := testkit.Config()
	c.Nodes[0].Location = &model.Location{Lat: 1, Lon: 2}
	c.Endpoints[0].Via = []string{"external"}
	c.Services[0].DependsOn = []string{"notes"}
	c.Paths[0].DependsOn = []string{"backup"}
	c.Paths[0].Proxies = []model.Proxy{{Address: "127.0.0.1:8080"}}
	x := &c.Checks[0]
	x.HTTP.Status = []int{200}
	x.HTTP.Headers = map[string]string{"Response": "yes"}
	x.HTTP.RequestHeaders = map[string]string{"X-Key": "private"}
	x.HTTP.ExpectedExit = []string{"192.0.2.1"}
	x.DNS.ExpectedIPs = []string{"192.0.2.2"}
	x.Adapter.ProviderReference = &model.MihomoProviderReference{URL: "https://publisher.invalid/members.json", Providers: []string{"dynamic"}, MaxAgeSeconds: 3600}
	x.Adapter.ExpectedProviders = map[string][]string{"provider": {"one", "two"}}
	before, _ := json.Marshal(c)
	copy := config.Clone(c)
	if !reflect.DeepEqual(c, copy) {
		t.Fatal("clone changed values")
	}
	// Mutate all reachable fields, including slices inside map values.
	var mutate func(reflect.Value)
	mutate = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if !v.IsNil() {
				mutate(v.Elem())
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				mutate(v.Field(i))
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				mutate(v.Index(i))
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				value := reflect.New(v.Type().Elem()).Elem()
				value.Set(v.MapIndex(k))
				mutate(value)
				v.SetMapIndex(k, value)
			}
		case reflect.String:
			if v.CanSet() {
				v.SetString("changed")
			}
		case reflect.Int:
			if v.CanSet() {
				v.SetInt(7)
			}
		case reflect.Float64:
			if v.CanSet() {
				v.SetFloat(8)
			}
		}
	}
	mutate(reflect.ValueOf(&copy).Elem())
	after, _ := json.Marshal(c)
	if string(before) != string(after) {
		t.Fatal("clone shares mutable storage")
	}
}
