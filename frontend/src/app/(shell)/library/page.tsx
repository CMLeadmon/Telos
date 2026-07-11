import { BookOpen } from "lucide-react";
import { ModulePlaceholder } from "@/components/ModulePlaceholder";

export default function LibraryPage() {
  return (
    <ModulePlaceholder
      icon={BookOpen}
      title="Library"
      banner="// one shelf for the whole node"
      headline="The shelf is being built."
      body="EPUBs, PDFs and audiobooks arrive with the Grimmory integration — reading in-app, annotating together."
    />
  );
}
