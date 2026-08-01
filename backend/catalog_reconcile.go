package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// CatalogEnumeration is a provider enumeration that completed successfully.
// Callers must not construct it from a failed, truncated, or partial response.
type CatalogEnumeration struct {
	Provider     CatalogProvider
	Libraries    []string
	Observations []CatalogObservation
}

type CatalogReconciliationReport struct {
	Observed int
	Scans    map[string]CatalogScanReport
	Backfill CatalogBackfillReport
}

type catalogEnumerationRepository interface {
	Observe(context.Context, CatalogObservation) (CatalogResolution, error)
	CompleteScan(context.Context, CatalogProvider, string, []string) (CatalogScanReport, error)
}

type catalogBackfillFunc func(context.Context, DBTX) (CatalogBackfillReport, error)

var reconcileCatalogEnumeration = func(ctx context.Context, enumeration CatalogEnumeration) (CatalogReconciliationReport, error) {
	if catalogRepo == nil || dbPool == nil {
		return CatalogReconciliationReport{}, errors.New("catalog reconciliation is not initialized")
	}
	return ReconcileCompleteCatalogEnumeration(ctx, catalogRepo, dbPool, enumeration, BackfillCatalogReferences)
}

var reconcileJellyfinCatalog = runJellyfinCatalogReconciliation

const (
	jellyfinEnumerationPageSize = 5000
	jellyfinEnumerationMaxItems = 100000
)

func runJellyfinCatalogReconciliation(ctx context.Context) (CatalogReconciliationReport, error) {
	started := time.Now()
	enumeration, err := enumerateJellyfinCatalog(ctx)
	if err != nil {
		_ = RecordCatalogEnumerationFailure(ProviderJellyfin, time.Since(started))
		return CatalogReconciliationReport{}, err
	}
	report, err := reconcileCatalogEnumeration(ctx, enumeration)
	if err != nil {
		return report, err
	}
	log.Printf("catalog reconciliation provider=jellyfin observed=%d scans=%d backfill_updated=%d", report.Observed, len(report.Scans), report.Backfill.Updated)
	return report, nil
}

func enumerateJellyfinCatalog(ctx context.Context) (CatalogEnumeration, error) {
	userID, err := getJellyfinUserID(ctx)
	if err != nil {
		return CatalogEnumeration{}, err
	}
	var views struct {
		Items []struct {
			ID string `json:"Id"`
		} `json:"Items"`
	}
	if err := jellyfinEnumerationGET(ctx, "/Users/"+url.PathEscape(userID)+"/Views", nil, &views); err != nil {
		return CatalogEnumeration{}, err
	}

	librarySet := map[string]struct{}{}
	if jellyfinAuthorizer != nil {
		for libraryID := range jellyfinAuthorizer.allowed {
			librarySet[libraryID] = struct{}{}
		}
	}
	observations := make([]CatalogObservation, 0)
	for _, view := range views.Items {
		if !jellyfinLibraryAllowed(view.ID) {
			continue
		}
		librarySet[view.ID] = struct{}{}
		observations = append(observations, CatalogObservation{
			Provider: ProviderJellyfin, UpstreamID: view.ID, LibraryID: view.ID,
			Surface: SurfaceStream, Kind: "folder",
		})

		startIndex := 0
		for {
			query := url.Values{
				"ParentId":   {view.ID},
				"Recursive":  {"true"},
				"Fields":     {"ChildCount"},
				"StartIndex": {strconv.Itoa(startIndex)},
				"Limit":      {strconv.Itoa(jellyfinEnumerationPageSize)},
			}
			var page struct {
				Items            []jellyfinChildItem `json:"Items"`
				TotalRecordCount int                 `json:"TotalRecordCount"`
			}
			if err := jellyfinEnumerationGET(ctx, "/Users/"+url.PathEscape(userID)+"/Items", query, &page); err != nil {
				return CatalogEnumeration{}, err
			}
			// The library root is also part of the completed scan, so reserve
			// one record for it when enforcing CompleteScan's safety bound.
			if startIndex+len(page.Items)+1 > jellyfinEnumerationMaxItems || page.TotalRecordCount+1 > jellyfinEnumerationMaxItems {
				return CatalogEnumeration{}, errors.New("Jellyfin catalog enumeration exceeded the safe record limit")
			}
			for _, item := range page.Items {
				kind, supported := jellyfinCatalogKind(item.Type, item.IsFolder)
				if !supported {
					continue
				}
				observations = append(observations, CatalogObservation{
					Provider: ProviderJellyfin, UpstreamID: item.ID, LibraryID: view.ID,
					Surface: SurfaceStream, Kind: kind,
				})
			}
			startIndex += len(page.Items)
			if len(page.Items) == 0 || page.TotalRecordCount == 0 || startIndex >= page.TotalRecordCount {
				break
			}
		}
	}

	libraries := make([]string, 0, len(librarySet))
	for libraryID := range librarySet {
		libraries = append(libraries, libraryID)
	}
	return CatalogEnumeration{Provider: ProviderJellyfin, Libraries: libraries, Observations: observations}, nil
}

func jellyfinEnumerationGET(ctx context.Context, path string, query url.Values, out any) error {
	requestCtx, cancel := upstreamRequestContext(ctx)
	defer cancel()
	target := jellyfinBaseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	token := getJellyfinAdminToken()
	req.Header.Set("X-Emby-Token", token)
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", token))
	resp, err := upstreamHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Jellyfin enumeration returned %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode Jellyfin enumeration: %w", err)
	}
	return nil
}

// ReconcileCompleteCatalogEnumeration records every observation before it
// completes any library scan. Backfill runs only after every completed scan,
// so it can resolve references against the newly observed source set.
func ReconcileCompleteCatalogEnumeration(
	ctx context.Context,
	repo catalogEnumerationRepository,
	db DBTX,
	enumeration CatalogEnumeration,
	backfill catalogBackfillFunc,
) (CatalogReconciliationReport, error) {
	report := CatalogReconciliationReport{Scans: make(map[string]CatalogScanReport)}
	if repo == nil || backfill == nil || !validCatalogProvider(enumeration.Provider) {
		return report, fmt.Errorf("%w: catalog enumeration", errCatalogInvalid)
	}

	seenByLibrary := make(map[string][]string, len(enumeration.Libraries))
	for _, observation := range enumeration.Observations {
		if observation.Provider != enumeration.Provider {
			return report, fmt.Errorf("%w: mixed providers", errCatalogInvalid)
		}
		if _, err := repo.Observe(ctx, observation); err != nil {
			return report, err
		}
		report.Observed++
		seenByLibrary[observation.LibraryID] = append(seenByLibrary[observation.LibraryID], observation.UpstreamID)
	}

	libraries := append([]string(nil), enumeration.Libraries...)
	sort.Strings(libraries)
	for _, libraryID := range libraries {
		scan, err := repo.CompleteScan(ctx, enumeration.Provider, libraryID, seenByLibrary[libraryID])
		if err != nil {
			return report, err
		}
		report.Scans[libraryID] = scan
	}

	backfillReport, err := backfill(ctx, db)
	report.Backfill = backfillReport
	if err != nil {
		return report, err
	}
	return report, nil
}
