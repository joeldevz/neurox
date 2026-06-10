/**
 * Neurox Provider Adapter
 * Implements MCP-compatible interface for Neurox HTTP API
 */

import fetch from 'node-fetch';
import { detectTemporalIntent } from '../utils.js';

/**
 * Helper: sleep for ms milliseconds
 */
function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

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
     * @param {Array} sessions - Array of conversation sessions
     * @param {string} namespace - Namespace for these observations
     * @param {Array} sessionDates - Optional array of dates (ISO 8601) corresponding to sessions
     * @param {Object} options - Options including ingestDelayMs for throttling
     */
  async ingest(sessions, namespace, sessionDates = [], options = {}) {
    const { ingestDelayMs = 0 } = options;
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

       // Build tags with optional date tag
       const tags = [`session-${i}`, 'longmemeval', 'haystack', 'conversation'];
       if (sessionDates && sessionDates[i]) {
         // Extract date in YYYY-MM-DD format and add as tag
         const dateStr = sessionDates[i];
         if (dateStr) {
           // Handle both slash (2023/05/20) and hyphen (2023-05-20) date formats
           const dateMatch = dateStr.match(/(\d{4})[\/-](\d{2})[\/-](\d{2})/);
           if (dateMatch) {
             tags.push(`date-${dateMatch[1]}-${dateMatch[2]}-${dateMatch[3]}`);
           }
         }
       }

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
            tags,
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
        
        // Apply throttling delay if specified
        if (ingestDelayMs > 0) {
          await sleep(ingestDelayMs);
        }
      } catch (err) {
        console.warn(`Warning: Exception ingesting session ${i}: ${err.message}`);
      }
    }
    return ingestedIds;
  }

/**
   * Format search results as LLM-ready context with metadata
   * @param {Array} results - Search results from Neurox API
   * @returns {string} Formatted context string
   */
  _formatAsLLMContext(results) {
    if (!results || results.length === 0) {
      return '';
    }

    const formatted = results
      .map((r, index) => {
        const rank = index + 1;
        const stalenessLabel = r.staleness || 'unknown';
        const kindLabel = r.kind || 'unknown';
        const obsTypeLabel = r.observation_type || 'unknown';
        const confidence = r.confidence ? r.confidence.toFixed(2) : 'unknown';
        const tags = (r.tags || []).join(', ') || 'none';

         // Extract date: prefer session date tag (event date), fall back to created_at
         let dateStr = '';
         if (r.tags) {
           // First priority: extract date from date-YYYY-MM-DD tag pattern (session/event date)
           const dateTag = r.tags.find(t => t.match(/^date-\d{4}-\d{2}-\d{2}$/));
           if (dateTag) {
             dateStr = dateTag.substring(5); // Remove 'date-' prefix
           }
         }
         if (!dateStr && r.created_at) {
           // Fall back to created_at (ingest date) only if no session date tag
           const dateObj = new Date(r.created_at);
           if (!isNaN(dateObj.getTime())) {
             dateStr = dateObj.toISOString().split('T')[0]; // YYYY-MM-DD
           }
         }

        let dateSection = '';
        if (dateStr) {
          dateSection = `\n    **Date**: ${dateStr}`;
        }

        return `${rank}. [Rank #${rank} | Kind: ${kindLabel} | Confidence: ${confidence} | Staleness: ${stalenessLabel}]
    **Title**: ${r.title || '(no title)'}
    **Tags**: ${tags}${dateSection}
    **Observation Type**: ${obsTypeLabel}
    **Content**:
    > ${r.content || '(no content)'}`;
      })
      .join('\n\n');

    return `**Retrieved Memories (${results.length})**\n\n${formatted}`;
  }

  /**
   * Sort results chronologically by session date (from date tag), with undated results last
   * @param {Array} results - Search results
   * @returns {Array} Sorted results
   */
  _sortBySessionDate(results) {
    const withDate = [];
    const withoutDate = [];
    
    for (const r of results) {
      const dateTag = (r.tags || []).find(t => t.match(/^date-\d{4}-\d{2}-\d{2}$/));
      if (dateTag) {
        const dateStr = dateTag.substring(5); // Remove 'date-' prefix
        withDate.push({ result: r, date: dateStr });
      } else {
        withoutDate.push(r);
      }
    }
    
    // Sort with-date results chronologically (ascending)
    withDate.sort((a, b) => a.date.localeCompare(b.date));
    
    // Combine: dated results first (chronologically), then undated (preserving relevance order)
    return [...withDate.map(d => d.result), ...withoutDate];
  }

  /**
   * Search for context given a query
   * Uses FTS5 semantic search endpoint
   */
   async search(query, namespace, limit = 5, options = {}) {
     const { contextFormat = 'raw', noTemporalBranch = false } = options;

     try {
       const params = new URLSearchParams({
         q: query,
         namespace,
         limit: String(limit),
       });

       // Detect temporal intent and add include_stale if temporal (CHANGE 6)
       const hasTemporalIntent = !noTemporalBranch && detectTemporalIntent(query);
       if (hasTemporalIntent) {
         params.append('include_stale', 'true');
       }

       const res = await fetch(`${this.baseUrl}/api/v1/observations/search?${params}`);
       if (!res.ok) {
         console.warn(`Search request failed: ${res.status} ${res.statusText}`);
         return '';
       }

       const data = await res.json();
       if (!data.results || data.results.length === 0) {
         return '';
       }

       let results = data.results;
       
       // Apply chronological sort if temporal intent detected (CHANGE 4)
       if (hasTemporalIntent) {
         results = this._sortBySessionDate(results);
       }

       // Format based on context format option
       if (contextFormat === 'llm') {
         return this._formatAsLLMContext(results);
       }

       // Default raw format: concatenate content only
       const context = results
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
