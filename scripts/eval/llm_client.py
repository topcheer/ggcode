"""LLM client wrapper for generating prompts and answering ask_user questions.

Uses ZAI (智谱) API for eval prompt generation and ask_user responses.
Reads configuration from environment variables or falls back to defaults.
"""

import os
import re
import time

from openai import OpenAI

# Default fallback answer when LLM fails or times out
FALLBACK_ANSWER = "Yes, proceed with your best judgment."

# ZAI default configuration
ZAI_DEFAULT_BASE_URL = "https://open.bigmodel.cn/api/coding/paas/v4"
ZAI_DEFAULT_MODEL = "glm-5-turbo"


def _load_zai_config_from_ref() -> tuple[str, str]:
    """Try to load ZAI API key from ggcode's reference config."""
    ref_paths = [
        "/tmp/knight-teamclaw-smoke3/baseline-config.yaml",
    ]
    for rp in ref_paths:
        try:
            with open(rp) as f:
                content = f.read()
            # Find the zai vendor section and extract api_key
            # Look for the pattern: display_name: Z.ai ... api_key: VALUE
            in_zai = False
            for line in content.split("\n"):
                if "display_name: Z.ai" in line:
                    in_zai = True
                if in_zai and "api_key:" in line and "${" not in line:
                    key = line.split("api_key:")[1].strip()
                    if key and not key.startswith("$"):
                        return key
        except Exception:
            pass
    return ""


class LLMClient:
    """Wraps an OpenAI-compatible API for eval prompt generation and ask_user responses."""

    def __init__(
        self,
        base_url: str | None = None,
        api_key: str | None = None,
        model: str = "",
        timeout: float = 30.0,
    ):
        self.model = model or os.getenv("ZAI_MODEL", ZAI_DEFAULT_MODEL)
        self.timeout = timeout

        # Resolve API key: explicit param > env var > ref config
        resolved_key = api_key or os.getenv("ZAI_API_KEY") or os.getenv("OPENAI_API_KEY")
        if not resolved_key:
            resolved_key = _load_zai_config_from_ref()

        resolved_base = base_url or os.getenv("ZAI_BASE_URL") or os.getenv("OPENAI_BASE_URL", ZAI_DEFAULT_BASE_URL)

        if not resolved_key:
            print("[llm] WARNING: No API key found. LLM calls will fail.")

        self.client = OpenAI(
            base_url=resolved_base,
            api_key=resolved_key or "dummy",
        )

    def generate_prompt(self, task_description: str) -> str:
        """Generate a concrete, actionable prompt from a task template description.

        Returns the generated prompt string. Falls back to the raw description on failure.
        """
        system = (
            "You are a software engineering task generator. Given a task description, "
            "generate a clear, specific, actionable prompt that a coding agent can execute. "
            "The prompt should be self-contained and include all necessary context. "
            "Output ONLY the prompt text, nothing else."
        )
        user = f"Generate a concrete coding prompt for this task:\n\n{task_description}"

        try:
            resp = self.client.chat.completions.create(
                model=self.model,
                messages=[
                    {"role": "system", "content": system},
                    {"role": "user", "content": user},
                ],
                max_tokens=1024,
                temperature=0.7,
                timeout=self.timeout,
            )
            text = resp.choices[0].message.content
            if text and text.strip():
                return text.strip()
        except Exception as e:
            print(f"[llm] prompt generation failed: {e}")

        # Fallback: use the raw description
        return task_description

    def answer_ask_user(self, question: str, context: str = "") -> str:
        """Generate an answer to an ask_user question from the coding agent.

        Returns the answer string. Falls back to FALLBACK_ANSWER on failure.
        """
        system = (
            "You are a senior software engineer answering a clarifying question from "
            "a coding agent. Give a brief, practical answer that helps the agent proceed. "
            "If unsure, give a reasonable default. Keep answers under 100 words. "
            "Output ONLY your answer, nothing else."
        )
        user_parts = [f"The agent asks:\n{question}"]
        if context:
            user_parts.append(f"\nContext:\n{context}")
        user_parts.append("\nProvide your answer:")

        try:
            resp = self.client.chat.completions.create(
                model=self.model,
                messages=[
                    {"role": "system", "content": system},
                    {"role": "user", "content": "\n".join(user_parts)},
                ],
                max_tokens=256,
                temperature=0.3,
                timeout=self.timeout,
            )
            text = resp.choices[0].message.content
            if text and text.strip():
                return text.strip()
        except Exception as e:
            print(f"[llm] ask_user answer generation failed: {e}")

        return FALLBACK_ANSWER
