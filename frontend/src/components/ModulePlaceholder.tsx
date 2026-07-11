import type { LucideIcon } from "lucide-react";
import { VaporwaveScene } from "@/components/VaporwaveScene";

export function ModulePlaceholder({
  icon: Icon,
  title,
  banner,
  headline,
  body,
}: {
  icon: LucideIcon;
  title: string;
  banner: string;
  headline: string;
  body: string;
}) {
  return (
    <>
      <VaporwaveScene />
      <div className="arenahead">
        <div className="name">
          <Icon size={17} />
          {title}
        </div>
      </div>
      <div className="banner">{banner}</div>
      <div className="placeholder">
        <span className="kicker">{"// under construction"}</span>
        <h2>{headline}</h2>
        <p>{body}</p>
      </div>
    </>
  );
}
