// A registry of the blob: URLs this client has handed to the document, so they
// can all be revoked when authority ends (logout, node switch). A blob URL made
// from a protected image outlives the request that fetched it; leaving it live
// after sign-out keeps the bytes reachable by anything that learned the URL.

const live = new Set<string>();

export function createTrackedObjectUrl(blob: Blob): string {
  const url = URL.createObjectURL(blob);
  live.add(url);
  return url;
}

export function revokeTrackedObjectUrl(url: string): void {
  if (live.delete(url)) URL.revokeObjectURL(url);
}

export function revokeAllTrackedObjectUrls(): void {
  for (const url of live) URL.revokeObjectURL(url);
  live.clear();
}

export function trackedObjectUrlCount(): number {
  return live.size;
}
