package config

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Les drapeaux de la ligne de commande arrivent dans la configuration
// (avant : liés sous « restate-url », jamais lus par restate_url).
func TestBindFlags_FlagsReachConfig(t *testing.T) {
	fs := pflag.NewFlagSet("watcher", pflag.ContinueOnError)
	fs.String("restate-url", "", "")
	fs.String("cluster-id", "", "")
	fs.String("key-file", "", "")
	if err := fs.Parse([]string{"--restate-url=http://api/clusters", "--cluster-id=dev-1", "--key-file=/k.json"}); err != nil {
		t.Fatal(err)
	}
	v := viper.New()
	if err := bindFlags(v, fs); err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.RestateURL != "http://api/clusters" || cfg.ClusterID != "dev-1" || cfg.KeyFile != "/k.json" {
		t.Fatalf("drapeaux ignorés : %+v", cfg)
	}
}
