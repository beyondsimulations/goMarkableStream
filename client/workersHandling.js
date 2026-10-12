// Reconnection state
let reconnectAttempts = 0;
// About half an hour of attempts, at most 15 s apart.
const MAX_RECONNECT_ATTEMPTS = 120;
let reconnectTimeout = null;
let isReconnecting = false;
// Refusals in a row (429: another connection holds the stream).
let rateLimitedRetries = 0;

// Names this page to the server, so the stream it opens again after a lost
// connection replaces its own old one, which the server may not have
// noticed is gone, instead of being refused, and so turning the touch block
// off ends this page's block and no other page's. A new id for each page
// load: a reloaded page closes its old connections properly.
const pageClientId = Array.from(crypto.getRandomValues(new Uint8Array(16)),
	(b) => b.toString(16).padStart(2, '0')).join('');

// Exponential backoff: 1s for attempt 0, doubling up to capMs.
function backoffDelay(attempt, capMs) {
	return Math.min(Math.pow(2, attempt) * 1000, capMs);
}

// Calculate reconnection delay with exponential backoff (2s, 4s, 8s, then
// every 15s)
function getReconnectDelay() {
	return backoffDelay(reconnectAttempts, 15000);
}

// Stops streaming: the worker and any attempt waiting to start one (the
// Funnel toggle frees the stream for public viewers).
function stopStreamWorker() {
	clearTimeout(reconnectTimeout);
	reconnectTimeout = null;
	reconnectAttempts = 0;
	rateLimitedRetries = 0;
	isReconnecting = false;
	hideReconnectBanner();
	streamWorker.terminate();
}

// Starts a fresh stream worker. The old one is ended first: its fetch would
// otherwise keep holding the stream against the new one.
function restartStreamWorker() {
	streamWorker.terminate();
	streamWorker = new Worker('worker_stream_processing.js');
	// Reset isReconnecting so next error can trigger another attempt
	isReconnecting = false;
	initStreamWorker();
}

// A refused stream is asked for again until it gets in (1s, 2s, 4s, 8s,
// then every 10s), without counting towards MAX_RECONNECT_ATTEMPTS: the
// server answered, and the other connection may be this page's own that the
// server has not dropped yet, or a viewer that leaves.
function retryRateLimited() {
	isReconnecting = true;
	hideReconnectBanner();
	const delay = backoffDelay(rateLimitedRetries, 10000);
	rateLimitedRetries++;
	clearTimeout(reconnectTimeout);
	reconnectTimeout = setTimeout(restartStreamWorker, delay);
}

// Attempt to reconnect the stream
function attemptStreamReconnect() {
	if (reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
		updateConnectionStatus(ConnectionState.DISCONNECTED);
		hideReconnectBanner();
		showConnectionError(
			'Unable to connect to the stream after multiple attempts. Please check your connection and try again.',
			true,
			() => {
				reconnectAttempts = 0;
				attemptStreamReconnect();
			}
		);
		isReconnecting = false;
		return;
	}

	isReconnecting = true;
	reconnectAttempts++;
	updateConnectionStatus(ConnectionState.RECONNECTING);
	showReconnectBanner(reconnectAttempts, MAX_RECONNECT_ATTEMPTS);

	const delay = getReconnectDelay();
	console.log(`Reconnecting in ${delay/1000}s (attempt ${reconnectAttempts}/${MAX_RECONNECT_ATTEMPTS})`);

	clearTimeout(reconnectTimeout);
	reconnectTimeout = setTimeout(restartStreamWorker, delay);
}

// Reset reconnection state on successful frame
function resetReconnectState() {
	if (isReconnecting || reconnectAttempts > 0) {
		console.log('Stream reconnected successfully');
		hideConnectionError();
		hideReconnectBanner();
	}
	reconnectAttempts = 0;
	rateLimitedRetries = 0;
	isReconnecting = false;
	if (reconnectTimeout) {
		clearTimeout(reconnectTimeout);
		reconnectTimeout = null;
	}
}

// Stream message handler as a named function for reuse
function handleStreamMessage(event) {
	const data = event.data;

	switch (data.type) {
		case 'update':
			// Reset reconnection state on successful frame
			resetReconnectState();
			updateConnectionStatus(ConnectionState.CONNECTED);

			// Handle the update
			const frameData = event.data.data;
			updateTexture(frameData, portrait, 1);
			break;
		case 'error':
			console.error('Error from worker:', event.data.message);

			// Check if this is a rate limit (429) error
			if (data.code === 'RATE_LIMITED') {
				updateConnectionStatus(ConnectionState.RATE_LIMITED);
				retryRateLimited();
				return;
			}

			// Check if this is a retryable error
			const isRetryable = data.retryable !== false;
			const severity = data.severity || 'error';

			if (severity === 'error' && isRetryable) {
				// Attempt automatic reconnection
				if (!isReconnecting) {
					attemptStreamReconnect();
				}
			} else if (severity === 'error') {
				// Non-retryable error
				updateConnectionStatus(ConnectionState.DISCONNECTED);
				showConnectionError(event.data.message, false);
			} else {
				// Warning or info level - just show message
				showMessage(event.data.message, MessageDuration.IMPORTANT);
			}
			break;
	}
}

// Initialize stream worker - reusable function for restart after Funnel toggle
function initStreamWorker() {
	// Attach handlers FIRST to catch any errors during init
	streamWorker.onmessage = handleStreamMessage;
	streamWorker.onerror = function(error) {
		console.error('Worker error:', error);
		if (!isReconnecting) {
			attemptStreamReconnect();
		}
	};
	streamWorker.postMessage({
		type: 'init',
		width: screenWidth,
		height: screenHeight,
		rate: rate,
		clientId: pageClientId,
		authToken: typeof getAuthToken === 'function' ? getAuthToken() : null,
	});
}

// Initialize on load
initStreamWorker();


// Determine the WebSocket protocol based on the current window protocol
const eventURL = `/events`;
// Send the OffscreenCanvas to the worker for initialization
eventWorker.postMessage({
	type: 'init',
	width: screenWidth,
	height: screenHeight,
	portrait: portrait,
	eventURL: eventURL,
    maxXValue: MaxXValue,
    maxYValue: MaxYValue,
	deviceModel: DeviceModel,
	authToken: typeof getAuthToken === 'function' ? getAuthToken() : null,
});
gestureWorker.postMessage({
	type: 'init',
	authToken: typeof getAuthToken === 'function' ? getAuthToken() : null,
	rotation: viewRotation(),
	clientId: pageClientId,
});

// Tells the embedded reveal.js deck to move: 'next' and 'prev' step through
// every fragment and slide in order, like the space bar.
// ('right' and 'left' would jump between horizontal slides, so in a Quarto
// deck with vertical stacks a whole chapter at a time.)
function sendSlideCommand(method) {
	document.getElementById('content').contentWindow.postMessage(JSON.stringify({ method }), '*');
}

// Chooses what is on top, over a presentation only: without one there is
// nothing to put on top, and the choice would carry over to the next one.
function swipeLayer(contentOnTop) {
	if (iFrame.getAttribute('src')) {
		setContentOnTop(contentOnTop);
	}
}

// Swipes as the person holding the tablet sees them. Right to left is the
// next slide, like turning a page on the tablet; the downward diagonals
// follow their sideways part, the upward ones choose what is on top.
const SWIPE_ACTIONS = {
	'swipe-left': () => sendSlideCommand('next'),
	'swipe-down-left': () => sendSlideCommand('next'),
	'swipe-right': () => sendSlideCommand('prev'),
	'swipe-down-right': () => sendSlideCommand('prev'),
	'swipe-up-right': () => swipeLayer(false), // drawing on top
	'swipe-up-left': () => swipeLayer(true), // content on top
};

gestureWorker.onmessage = (event) => {
	const data = event.data;

	switch (data.type) {
		case 'gesture': {
			const action = SWIPE_ACTIONS[event.data.value];
			if (action) {
				action();
			}
			break;
		}
		case 'touchBlock':
			touchBlockAnswered(data.blocked, data.error, data.elsewhere);
			break;
		case 'touchBlockLost':
			touchBlockLost();
			break;
		case 'error':
			console.error('Error from worker:', event.data.message);
			break;
	}

}

let messageTimeout;

function clearLaser() {
	// Function to call when no message is received for 300 ms
	updateLaserPosition(-10,-10);
}
// Event worker connection state
let eventWorkerConnected = false;

// Listen for updates from the worker
eventWorker.onmessage = (event) => {
	// Reset the timer every time a message is received
	clearTimeout(messageTimeout);
	messageTimeout = setTimeout(clearLaser, 300);

	const data = event.data;

	switch (data.type) {
		case 'connected':
			// SSE connection established
			eventWorkerConnected = true;
			console.log('Event worker connected');
			break;
		case 'clear':
			updateLaserPosition(-10,-10);
			//clearLaser();
			break;
		case 'update':
			// Mark as connected on first update if not already
			if (!eventWorkerConnected) {
				eventWorkerConnected = true;
			}
			// Handle the update
			const X = event.data.X;
			const Y = event.data.Y;
			updateLaserPosition(X,Y);
			break;
		case 'error':
			console.error('Error from event worker:', event.data.message);
			eventWorkerConnected = false;
			// Don't show UI errors for event worker - stream worker handles primary status
			// Just log to console
			break;
	}
};
