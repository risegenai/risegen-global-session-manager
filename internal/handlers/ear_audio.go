package handlers

import "encoding/json"

// EarAudioTTLSeconds is seven days in seconds. The audio_base64 column
// expires after this duration; the event row remains.
const EarAudioTTLSeconds = 604800

const earAudioColumnCQL = `UPDATE global_sessions.global_session_events SET audio_base64 = ? WHERE scope_id = ? AND session_id = ? AND seq = ? USING TTL 604800`

// prepareEarAudio extracts audio_base64 from an ear.audio payload so it can
// be stored in a dedicated column with a TTL. The returned stored payload has
// the key removed and metadata added. writeColumn is true only when a
// non-empty audio string was extracted.
func prepareEarAudio(eventType, payload string) (stored string, audio string, writeColumn bool) {
	if eventType != "ear.audio" {
		return payload, "", false
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return payload, "", false
	}

	audioVal, _ := obj["audio_base64"].(string)
	delete(obj, "audio_base64")

	obj["audio_ttl_seconds"] = EarAudioTTLSeconds
	obj["audio_stored"] = audioVal != ""

	out, err := json.Marshal(obj)
	if err != nil {
		return payload, "", false
	}
	return string(out), audioVal, audioVal != ""
}

// mergeEarAudio recombines the TTL'd audio column with the event payload on
// read. When the column is empty but audio_stored is true the bytes have
// expired and audio_expired is set.
func mergeEarAudio(eventType, payload, column string) string {
	if eventType != "ear.audio" {
		return payload
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return payload
	}

	if column != "" {
		obj["audio_base64"] = column
		obj["audio_expired"] = false
	} else if stored, ok := obj["audio_stored"].(bool); ok && stored {
		obj["audio_expired"] = true
		delete(obj, "audio_base64")
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return string(out)
}

// markEarAudioStoreFailed annotates a payload when the TTL'd column write
// fails so readers know the audio was never persisted.
func markEarAudioStoreFailed(payload string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(payload), &obj); err != nil {
		return payload
	}

	obj["audio_stored"] = false
	obj["audio_store_error"] = "ttl_write_failed"

	out, err := json.Marshal(obj)
	if err != nil {
		return payload
	}
	return string(out)
}
