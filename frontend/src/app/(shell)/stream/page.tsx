import { Tv } from "lucide-react";
import { ModulePlaceholder } from "@/components/ModulePlaceholder";

export default function StreamPage() {
  return (
    <ModulePlaceholder
      icon={Tv}
      title="Stream"
      banner="// jellyfin, reimagined"
      headline="The projector is warming up."
      body="Films and shows off the node land here — HLS playback wired to /api/v1/stream is the next build phase."
    />
  );
}
