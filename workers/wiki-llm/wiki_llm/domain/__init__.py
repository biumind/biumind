"""Pure-function domain modules for the wiki ingest pipeline.

These modules are deliberately I/O-free so they can be tested without
NATS / Postgres / LLM mocks. Lands incrementally:

    P1-4  path_safety   — wiki path whitelist + traversal guard
    P1-5  frontmatter   — YAML frontmatter parse + serialize
    P1-6  wikilink      — [[link]] / [[link|alias]] parse + render
    P1-7  ingest_parse  — streaming ---FILE: path---/---END FILE--- parser

Re-implemented from llm_wiki TS source (commit 0.4.x) under Apache-2.0
to keep biumind license-clean. Test fixtures may reference the same
input strings as knowcode's tests because string fixtures aren't
copyrightable, but the implementation is independent.
"""
