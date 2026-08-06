package main

import (
	"context"
	"errors"
)

var errCatalogTargetNotFound = errors.New("catalog target not found")

func resolveAuthorizedCatalogTarget(ctx context.Context, targetType, rawID string) (CatalogResolution, error) {
	resolution, err := canonicalAnnotationTarget(ctx, targetType, rawID)
	if err != nil {
		return CatalogResolution{}, errCatalogTargetNotFound
	}
	if err := authorizeResolvedCatalogTarget(ctx, targetType, resolution); err != nil {
		return CatalogResolution{}, errCatalogTargetNotFound
	}
	return resolution, nil
}

// authorizeResolvedCatalogTarget applies the complete current-target gate used
// by commentary and durable chat embeds. A canonical UUID is only an identity:
// the active source must remain available, on the expected surface/provider,
// allowed by current provider configuration, and visible to this member.
func authorizeResolvedCatalogTarget(ctx context.Context, targetType string, resolution CatalogResolution) error {
	if !resolution.Active || !resolution.Available || !looksLikeUUID(resolution.ID) {
		return errCatalogTargetNotFound
	}
	user, _ := ctx.Value(userContextKey).(*UserContext)
	if user == nil {
		return errCatalogTargetNotFound
	}

	switch targetType {
	case "book":
		if resolution.Surface != SurfaceLibrary || resolution.Provider != ProviderGrimmory {
			return errCatalogTargetNotFound
		}
		if grimmoryAuthorizer != nil {
			authorized, err := grimmoryAuthorizer.AuthorizeBook(ctx, user, resolution.UpstreamID, BookRead)
			if err != nil || authorized.LibraryID != resolution.LibraryID {
				return errCatalogTargetNotFound
			}
			return nil
		}
		allowed, err := hasPermission(ctx, user, "view_library", nil)
		if err != nil || !allowed {
			return errCatalogTargetNotFound
		}
		return nil

	case "media":
		if resolution.Surface != SurfaceStream || resolution.Provider != ProviderJellyfin {
			return errCatalogTargetNotFound
		}
		allowed, err := hasPermission(ctx, user, "view_media", nil)
		if err != nil || !allowed {
			return errCatalogTargetNotFound
		}
		if jellyfinAuthorizer != nil {
			authorized, err := jellyfinAuthorizer.AuthorizeItem(ctx, resolution.UpstreamID)
			if err != nil || authorized.LibraryID != resolution.LibraryID {
				return errCatalogTargetNotFound
			}
		}
		return nil
	default:
		return errCatalogTargetNotFound
	}
}
