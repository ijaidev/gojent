"use client";

import { useEffect, useRef } from "react";
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

  useEffect(() => {
    stickToBottom.current = true;
    const container = scrollRef.current;
    if (container) {
      container.scrollTop = container.scrollHeight;
    }
  }, []);

  useEffect(() => {
    const container = scrollRef.current;
    if (!container) {
      return;
    }
    const scrollToBottom = () => {
      if (stickToBottom.current) {
        container.scrollTop = container.scrollHeight;
      }
    };
    const observer = new ResizeObserver(scrollToBottom);
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
      <div
        ref={scrollRef}
        className="min-h-0 flex-1 overflow-y-auto"
        onScroll={() => {
          const container = scrollRef.current;
          if (!container) {
            return;
          }
          const distance =
            container.scrollHeight -
            container.scrollTop -
            container.clientHeight;
          stickToBottom.current = distance < 140;
        }}
      >
        <Transcript
          live={live}
          messages={messages}
          onResolveApproval={onResolveApproval}
          workspaceName={workspaceName}
        />
      </div>

      <div className="shrink-0 border-t border-line/70 bg-app/95 px-4 pb-3 pt-3 backdrop-blur sm:px-6">
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
