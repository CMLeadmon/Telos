import { Folder } from "lucide-react";
import { ModulePlaceholder } from "@/components/ModulePlaceholder";

export default function FilesPage() {
  return (
    <ModulePlaceholder
      icon={Folder}
      title="Files"
      banner="// nothing leaves the node unless you send it"
      headline="Shared storage, coming into focus."
      body="Uploads, virus-scanned drops and the shared library UI wire up to /api/v1/files in a later pass."
    />
  );
}
