"use client";

import { ArrowDown } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Composer } from "@/components/composer";
import { Transcript } from "@/components/transcript";
import type { LiveRun, SendMessage } from "@/hooks/use-codjent";
import type {
  Message,
  ProviderInfo,
  Thread,
  UpdateThreadInput,
} from "@/lib/types";
import { isActiveStatus } from "@/lib/types";

interface ThreadViewProps {
  thread: Thread;
  messages: Message[];
  live?: LiveRun;
  providers: ProviderInfo[];
  workspaceName: string;
  sending: boolean;
  onSend: SendMessage;
  onStop: () => void;
  onResolveApproval: (approvalID: string, allow: boolean) => Promise<boolean>;
  onUpdateThread: (input: UpdateThreadInput) => Promise<Thread | null>;
}

export function ThreadView({
  thread,
  messages,
  live,
  providers,
  workspaceName,
  sending,
  onSend,
  onStop,
  onResolveApproval,
  onUpdateThread,
}: ThreadViewProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const stickToBottom = useRef(true);
  const [showJump, setShowJump] = useState(false);

  const jumpToLatest = () => {
    const container = scrollRef.current;
    if (!container) {
      return;
    }
    stickToBottom.current = true;
    setShowJump(false);
    container.scrollTop = container.scrollHeight;
  };

  useEffect(() => {
    stickToBottom.current = true;
    const container = scrollRef.current;
    if (container) {
      container.scrollTop = container.scrollHeight;
    }
    setShowJump(false);
  }, []);

  useEffect(() => {
    const container = scrollRef.current;
    if (!container) {
      return;
    }
    const onResize = () => {
      if (stickToBottom.current) {
        container.scrollTop = container.scrollHeight;
      } else {
        setShowJump(true);
      }
    };
    const observer = new ResizeObserver(onResize);
    observer.observe(container);
    const content = container.firstElementChild;
    if (content) {
      observer.observe(content);
    }
    return () => observer.disconnect();
  }, []);

  const busy = Boolean(live && isActiveStatus(live.status));

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="relative min-h-0 flex-1">
        <div
          className="h-full overflow-y-auto"
          onScroll={() => {
            const container = scrollRef.current;
            if (!container) {
              return;
            }
            const distance =
              container.scrollHeight -
              container.scrollTop -
              container.clientHeight;
            const next = distance < 140;
            stickToBottom.current = next;
            setShowJump(!next);
          }}
          ref={scrollRef}
        >
          <Transcript
            live={live}
            messages={messages}
            onResolveApproval={onResolveApproval}
            workspaceName={workspaceName}
          />
        </div>

        {showJump ? (
          <button
            aria-label="Jump to latest"
            className="absolute bottom-4 left-1/2 inline-flex min-h-11 -translate-x-1/2 items-center gap-1.5 rounded-full border border-line bg-surface px-4 py-1.5 text-meta font-medium text-muted shadow-overlay transition-colors duration-fast hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 sm:min-h-0 sm:px-3"
            onClick={jumpToLatest}
            type="button"
          >
            <ArrowDown className="size-3.5" />
            Latest
          </button>
        ) : null}
      </div>

      <div className="shrink-0 border-t border-line bg-app px-4 pb-3 pt-3 sm:px-6">
        <Composer
          autoFocus
          busy={busy}
          key={thread.id}
          large={false}
          liveStatus={live?.status}
          onSend={onSend}
          onStop={onStop}
          onUpdateThread={onUpdateThread}
          providers={providers}
          sending={sending}
          thread={thread}
          workspaceName={workspaceName}
        />
      </div>
    </div>
  );
}
