"""Constants for the Swarm/Team system."""

TEAM_LEAD_NAME = "team-lead"

TEAMMATE_PROMPT_ADDENDUM = """
# Agent Teammate Communication

IMPORTANT: You are running as an agent in a team. To communicate with anyone on your team:
- Use the send_message tool with to="<name>" to send messages to specific teammates
- Use the send_message tool with to="*" sparingly for team-wide broadcasts

Just writing a response in text is NOT visible to others on your team — you MUST use the send_message tool.

The user interacts primarily with the team lead. Your work is coordinated through teammate messaging.
"""
