import type { AppNotification, NotificationKind } from "@/stores/useNotificationStore";

const KIND_LABEL: Record<NotificationKind, string> = {
  mention: "mentioned you",
  thread_reply: "replied in a thread",
  annotation_reply: "replied to your annotation",
  watch_party_invite: "invited you to a Watch Party",
  account_security: "account security update",
};

// sourceHref maps a notification to its currently authorized source context. A
// notification whose source has been removed or is no longer accessible lands on
// the module home, which explains the source is unavailable rather than 404ing
// blindly. Book resources are re-authorized server-side on navigation.
function sourceHref(n: AppNotification): string | null {
  switch (n.kind) {
    case "mention":
    case "thread_reply":
      return n.resourceId ? `/chat/?message=${encodeURIComponent(n.resourceId)}` : "/chat/";
    case "annotation_reply":
      return n.resourceId ? `/library/?book=${encodeURIComponent(n.resourceId)}` : "/library/";
    case "watch_party_invite":
      return n.resourceId ? `/stream/?party=${encodeURIComponent(n.resourceId)}` : "/stream/";
    case "account_security":
      return "/settings/";
    default:
      return null;
  }
}

export function NotificationItem({
  notification,
  onActivate,
}: {
  notification: AppNotification;
  onActivate: (n: AppNotification) => void;
}) {
  const href = sourceHref(notification);
  const label = KIND_LABEL[notification.kind] ?? "notification";
  return (
    <li
      className={`notif-item${notification.read ? "" : " unread"}`}
      role="option"
      aria-selected={!notification.read}
    >
      <a
        className="notif-link"
        href={href ?? undefined}
        onClick={() => onActivate(notification)}
        data-testid={`notif-${notification.id}`}
      >
        <span className="notif-dot" aria-hidden="true" />
        <span className="notif-body">
          <span className="notif-label">{label}</span>
          <time className="notif-time" dateTime={notification.createdAt}>
            {new Date(notification.createdAt).toLocaleString()}
          </time>
        </span>
      </a>
    </li>
  );
}
