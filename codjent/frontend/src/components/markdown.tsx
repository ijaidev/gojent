import { type ComponentProps, memo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

export const Markdown = memo(function Markdown({
  children,
}: {
  children: string;
}) {
  return (
    <div className="markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ href, children: linkChildren, node: _node, ...props }) => {
            const external = href?.startsWith("http");
            return (
              <a
                href={href}
                {...(external
                  ? { target: "_blank", rel: "noreferrer noopener" }
                  : {})}
                {...(props as ComponentProps<"a">)}
              >
                {linkChildren}
              </a>
            );
          },
        }}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
});
