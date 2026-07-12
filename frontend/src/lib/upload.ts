// XHR-based multipart upload. The shared api() fetch helper can't report
// upload progress and forces a JSON content type, so uploads go through here.
// Phases: "uploading" (0-100% of bytes sent) then "scanning" (server-side
// hash + ClamAV pass before it responds).

import { apiBase } from "@/lib/api";

export type UploadPhase = "uploading" | "scanning";

export interface UploadOutcome {
  id: string;
  filename: string;
  sha256: string;
  scan_status: string;
}

export class UploadError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

export function uploadFile(
  path: string,
  file: File,
  onProgress: (phase: UploadPhase, percent: number) => void,
): Promise<UploadOutcome> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", `${apiBase()}${path}`);
    xhr.withCredentials = true;
    xhr.upload.onprogress = (e) => {
      if (!e.lengthComputable) return;
      const percent = Math.round((e.loaded / e.total) * 100);
      onProgress(percent >= 100 ? "scanning" : "uploading", percent);
    };
    xhr.upload.onload = () => onProgress("scanning", 100);
    xhr.onerror = () =>
      reject(new UploadError(0, "network error during upload"));
    xhr.onload = () => {
      if (xhr.status === 201) {
        try {
          resolve(JSON.parse(xhr.responseText) as UploadOutcome);
        } catch {
          reject(new UploadError(xhr.status, "malformed upload response"));
        }
      } else {
        reject(
          new UploadError(
            xhr.status,
            xhr.responseText.trim() || xhr.statusText,
          ),
        );
      }
    };
    const form = new FormData();
    form.append("file", file);
    xhr.send(form);
  });
}
