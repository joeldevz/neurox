/**
 * Neurox Provider Adapter
 * Implements MCP-compatible interface for Neurox HTTP API
 */

import fetch from 'node-fetch';

export class NeuroxProvider {
  constructor(baseUrl = 'http://localhost:7438') {
    this.baseUrl = baseUrl;
    this.sessionId = null;
    this.observations = new Map(); // id -> observation
  }

  /**
   * Check if Neurox API is available
   */
  async health() {
    try {
      const res = await fetch(`${this.baseUrl}/api/v1/status`);
      return res.ok;
    } catch {
      return false;
    }
  }

  /**
   * Initialize a new session
   */
  async initialize(namespace) {
    this.namespace = namespace;
    try {
      const res = await fetch(`${this.baseUrl}/api/v1/sessions`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: `MemBench Session ${new Date().toISOString()}`,
          namespace,
        }),
      });
      const data = await res.json();
      this.sessionId = data.id;
      return data;
    } catch (err) {
      throw new Error(`Failed to initialize session: ${err.message}`);
    }
  }

  /**
   * Ingest sessions as observations into Neurox
   * Each session becomes an episodic observation
   */
  async ingest(sessions, namespace) {
    const ingestedIds = [];
    for (let i = 0; i < sessions.length; i++) {
      const session = sessions[i];
      
      // Format session as user-assistant pairs with better structure
      const parts = [];
      for (let j = 0; j < session.length; j++) {
        const msg = session[j];
        const prefix = msg.role === 'user' ? 'User' : 'Assistant';
        parts.push(`${prefix}: ${msg.content}`);
      }
      const content = parts.join('\n\n');

      // Create descriptive title that captures conversation essence
      const firstUserMsg = session.find((m) => m.role === 'user')?.content || '';
      const firstAssistantMsg = session.find((m) => m.role === 'assistant')?.content || '';
      
      // Use keywords from messages for better indexing
      const titleParts = [];
      if (firstUserMsg) titleParts.push(firstUserMsg.substring(0, 60));
      if (firstAssistantMsg) titleParts.push(firstAssistantMsg.substring(0, 40));
      
      const title = titleParts.length > 0 
        ? titleParts.join(' | ')
        : `Session ${i}`;

      try {
        const res = await fetch(`${this.baseUrl}/api/v1/observations`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            title,
            content,
            namespace,
            kind: 'episodic',
            observation_type: 'discovery',
            tags: [`session-${i}`, 'longmemeval', 'haystack', 'conversation'],
            confidence: 0.8,
          }),
        });

        if (!res.ok) {
          const error = await res.text();
          console.warn(`Warning: Failed to ingest session ${i}: ${error}`);
          continue;
        }

        const data = await res.json();
        ingestedIds.push(data.id);
        this.observations.set(data.id, { title, content, index: i });
      } catch (err) {
        console.warn(`Warning: Exception ingesting session ${i}: ${err.message}`);
      }
    }
    return ingestedIds;
  }

  /**
   * Search for context given a query
   * Uses FTS5 semantic search endpoint
   */
  async search(query, namespace, limit = 5) {
    try {
      const params = new URLSearchParams({
        q: query,
        namespace,
        limit: String(limit),
      });

      const res = await fetch(`${this.baseUrl}/api/v1/observations/search?${params}`);
      if (!res.ok) {
        console.warn(`Search request failed: ${res.status} ${res.statusText}`);
        return '';
      }

      const data = await res.json();
      if (!data.results || data.results.length === 0) {
        return '';
      }

      // Aggregate results into context string
      // Each result has: id, title, content, score, ...
      const context = data.results
        .map((r) => r.content)
        .join('\n\n---\n\n');

      return context;
    } catch (err) {
      console.warn(`Search error: ${err.message}`);
      return '';
    }
  }

  /**
   * Clear all observations in a namespace
   */
  async clear(namespace) {
    try {
      const params = new URLSearchParams({
        q: '*',
        namespace,
        limit: '1000',
      });

      const res = await fetch(`${this.baseUrl}/api/v1/observations/search?${params}`);
      if (!res.ok) throw new Error('Failed to list observations');

      const data = await res.json();
      if (!data.results) return;

      const results = data.results;
      for (const obs of results) {
        try {
          await fetch(`${this.baseUrl}/api/v1/observations/${obs.id}`, {
            method: 'DELETE',
          });
        } catch (err) {
          console.warn(`Failed to delete ${obs.id}: ${err.message}`);
        }
      }

      console.log(`Cleared ${results.length} observations from namespace: ${namespace}`);
    } catch (err) {
      throw new Error(`Clear failed: ${err.message}`);
    }
  }

  /**
   * End session
   */
  async endSession(summary) {
    if (!this.sessionId) return;
    try {
      await fetch(`${this.baseUrl}/api/v1/sessions/${this.sessionId}/end`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ summary }),
      });
    } catch (err) {
      console.warn(`Failed to end session: ${err.message}`);
    }
  }
}
