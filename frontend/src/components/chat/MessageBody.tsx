"use client";

import React from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeSanitize, { defaultSchema } from "rehype-sanitize";

const schema = {
  ...defaultSchema,
  tagNames: ["p", "strong", "em", "code", "pre", "a", "ul", "ol", "li", "blockquote", "br"],
  attributes: {
    ...defaultSchema.attributes,
    a: ["href"],
  },
};

interface MessageBodyProps {
  content: string;
}

export function MessageBody({ content }: MessageBodyProps) {
  return (
    <div className="mbody">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[[rehypeSanitize, schema]]}
        components={{
          a: ({ href, children }) => (
            <a href={href} target="_blank" rel="noreferrer noopener">
              {children}
            </a>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
