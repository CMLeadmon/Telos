// Node-relative paths for protected resources. Pure strings, no network and no
// configuration, so any module (and any test that mocks the API client) can use
// them. A path is not a URL: the authenticated client or AuthenticatedImage turns
// it into a request against the configured node.

export function avatarPath(userId: string): string {
  return `/api/v1/users/${userId}/avatar`;
}

export function libraryCoverPath(bookId: string): string {
  return `/api/v1/library/books/${encodeURIComponent(bookId)}/cover`;
}
