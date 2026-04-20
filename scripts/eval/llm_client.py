"""LLM client wrapper for generating prompts and answering ask_user questions.

Supports OpenAI-compatible APIs (including ZAI/GLM). Used by the eval
orchestrator to:
1. Generate concrete prompts from task templates
2. Generate intelligent responses to ask_user questions
"""

import os
import time

from openai import OpenAI

# Default fallback answer when LLM fails or times out
FALLBACK_ANSWER = "Yes, proceed with your best judgment."


class LLMClient:
    """Wraps an OpenAI-compatible API for eval prompt generation and ask_user responses."""

    def __init__(
        self,
        base_url: str | None = None,
        api_key: str | None = None,
        model: str = "glm-5-turbo",
        timeout: float = 30.0,
    ):
        self.model = model
        self.timeout = timeout
        self.client = OpenAI(
            base_url=base_url or os.getenv("OPENAI_BASE_URL", "https://open.bigmodel.cn/api/paas/v4"),
            api_key=api_key or os.getenv("OPENAI_API_KEY", ""),
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
