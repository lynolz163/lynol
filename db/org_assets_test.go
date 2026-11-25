package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Autumn-27/artex/internal/org"
	"github.com/google/uuid"
)

func organizationAssetRules(t *testing.T, d *DB) (int64, string, string) {
	t.Helper()
	key := uuid.NewString()
	data, _ := json.Marshal(map[string]any{"name": "资产归属测试-" + key, "code": key, "active": true})
	department, err := org.New(d.DB).Change(t.Context(), org.Command{
		Kind: "department", Action: "save", Data: data, Scope: "all_open", IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	domain := "org-" + key + ".example.com"
	ip := "2001:db8:" + key[:4] + ":" + key[4:8] + "::1"
	for _, match := range []struct{ kind, value string }{{"domain", domain}, {"ip_cidr", ip + "/128"}} {
		body, _ := json.Marshal(map[string]any{
			"rule": map[string]any{"match_type": match.kind, "match_value": match.value,
				"department_id": department.EntityID, "business_line_id": nil, "priority": 20, "active": true},
			"idempotency_key": uuid.NewString(),
		})
		r := httptest.NewRequest(http.MethodPost, "/api/org/rules/save", bytes.NewReader(body))
		w := httptest.NewRecorder()
		org.Handler(d.DB).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("save rule: status %d: %s", w.Code, w.Body.String())
		}
	}
	return department.EntityID, domain, ip
}

func TestOrganizationAssetWritesRegisterAndAssign(t *testing.T) {
	d, assets, _ := testSetup(t)
	defer d.Close()
	department, domain, ip := organizationAssetRules(t, d)
	writes := []struct {
		name     string
		write    func() (int64, error)
		assigned bool
	}{
		{"root domain", func() (int64, error) { return assets.UpsertRootDomain(UpsertRootDomainReq{Domain: domain}) }, true},
		{"IP", func() (int64, error) { return assets.UpsertIP(UpsertIPReq{IP: ip}) }, true},
		{"subdomain", func() (int64, error) { return assets.UpsertSubdomain(UpsertSubdomainReq{Domain: domain}) }, true},
		{"HTTP service", func() (int64, error) { return assets.UpsertHTTPService(UpsertHTTPServiceReq{URL: "https://" + domain}) }, true},
		{"IP URL service", func() (int64, error) {
			return assets.UpsertHTTPService(UpsertHTTPServiceReq{URL: "http://[" + ip + "]:8080"})
		}, true},
		{"other service", func() (int64, error) {
			return assets.UpsertOtherService(UpsertOtherServiceReq{IP: ip, Port: 22, ServiceName: "SSH"})
		}, true},
		{"endpoint", func() (int64, error) {
			return assets.UpsertEndpoint(UpsertEndpointReq{URL: "https://" + domain + "/test", Method: "GET"})
		}, true},
		{"application", func() (int64, error) { return assets.UpsertApp(UpsertAppReq{Name: "org-" + uuid.NewString()}) }, false},
		{"IP URL endpoint", func() (int64, error) {
			return assets.UpsertEndpoint(UpsertEndpointReq{URL: "http://[" + ip + "]/test", Method: "GET"})
		}, true},
	}
	for _, test := range writes {
		t.Run(test.name, func(t *testing.T) {
			id, err := test.write()
			if err != nil {
				t.Fatal(err)
			}
			var registered bool
			if err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM org.asset_registry WHERE asset_id=$1)`, id).Scan(&registered); err != nil || !registered {
				t.Fatalf("registration missing for %d: %v", id, err)
			}
			var count int
			if err := d.QueryRow(`SELECT count(*) FROM org.asset_ownership WHERE asset_id=$1 AND department_id=$2 AND source='rule'`, id, department).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if test.assigned {
				want = 1
			}
			if count != want {
				t.Fatalf("rule ownership count %d, want %d", count, want)
			}
			if _, err = test.write(); err != nil {
				t.Fatal(err)
			}
			if err := d.QueryRow(`SELECT count(*) FROM org.asset_ownership WHERE asset_id=$1`, id).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != want {
				t.Fatalf("duplicate write created extra history: %d", count)
			}
		})
	}
	var missing int
	if err := d.QueryRow(`SELECT count(*) FROM assets a LEFT JOIN org.asset_registry r ON r.asset_id=a.id WHERE (a.domain=$1 OR a.ip=$2 OR a.domain='example.com') AND r.asset_id IS NULL`, domain, ip).Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("nested host/IP assets were not registered: %d, %v", missing, err)
	}
}

func TestOrganizationAssetRollbackIsAtomic(t *testing.T) {
	d, assets, _ := testSetup(t)
	defer d.Close()
	_, domain, _ := organizationAssetRules(t, d)
	rollback := errors.New("roll back synthetic asset transaction")
	var assetID int64
	_, err := assets.withCompanyScopeMutation(func(scoped *AssetStore) (int64, error) {
		var writeErr error
		assetID, writeErr = scoped.UpsertRootDomain(UpsertRootDomainReq{Domain: domain})
		if writeErr != nil {
			return 0, writeErr
		}
		return assetID, rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	for _, table := range []string{"public.assets", "org.asset_registry", "org.asset_ownership"} {
		column := "asset_id"
		if table == "public.assets" {
			column = "id"
		}
		var count int
		if err := d.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s WHERE %s=$1", table, column), assetID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rollback left rows in %s: %d, %v", table, count, err)
		}
	}
}

func TestOrganizationAssetArchiveRestoreRegisters(t *testing.T) {
	d, _, _ := testSetup(t)
	defer d.Close()
	department, domain, _ := organizationAssetRules(t, d)
	var id int64
	if err := d.QueryRow(`INSERT INTO assets(type,domain,root_domain) VALUES('root_domain',$1,$1) RETURNING id`, domain).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var archive []byte
	if err := d.QueryRow(`SELECT jsonb_agg(a) FROM assets a WHERE id=$1`, id).Scan(&archive); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`DELETE FROM assets WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	mapping, _, err := restoreArchiveAssets(tx, 1, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var got int64
	if err := d.QueryRow(`SELECT department_id FROM org.asset_ownership WHERE asset_id=$1 AND valid_to IS NULL`, mapping[id]).Scan(&got); err != nil || got != department {
		t.Fatalf("restored asset attribution: %d, %v", got, err)
	}
}
