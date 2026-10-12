// Constants for the maximum values from the WebSocket messages
const SWIPE_DISTANCE = 200;
let authToken = null;
// How far, clockwise, the page turns the tablet's portrait view (0, 90, 180
// or 270). The Paper Pro and the Paper Pro Move name each swipe for it.
let rotation = 0;
let running = false;
let controller = null;
// Time before the next attempt after the stream ends or fails; it doubles
// up to 30 seconds and starts again at one once a stream opens.
let retryDelay = 1000;

// The reMarkable 2 sends direction totals, which checkSwipeDirection names;
// this gives them the names the Paper Pro sends. Each legacy name stands for
// its two totals: "topleft-to-bottomright" is left and down, a move down
// and to the left.
const LEGACY_SWIPES = {
	'left': 'swipe-left',
	'right': 'swipe-right',
	'up': 'swipe-up',
	'down': 'swipe-down',
	'topleft-to-bottomright': 'swipe-down-left',
	'topright-to-bottomleft': 'swipe-down-right',
	'bottomleft-to-topright': 'swipe-up-left',
	'bottomright-to-topleft': 'swipe-up-right',
};

onmessage = (event) => {
	const data = event.data;

	switch (data.type) {
		case 'init':
			authToken = event.data.authToken || null;
			rotation = event.data.rotation || 0;
			running = true;
			streamGestures();
			break;
		case 'rotation':
			// Reconnect, so the server reads swipes for the new view.
			if (data.rotation !== rotation) {
				rotation = data.rotation;
				if (controller) {
					controller.abort();
				}
			}
			break;
		case 'terminate':
			console.log("terminating worker");
			running = false;
			if (controller) {
				controller.abort();
			}
			close();
			break;
	}
};

// Keeps the gesture stream open: again at once after a rotation change, and
// after a growing pause when the stream ends or fails. A refused sign-in
// stops it, since the same token would be refused again.
async function streamGestures() {
	while (running) {
		controller = new AbortController();
		try {
			await fetchStream(controller.signal);
		} catch (e) {
			if (e.status === 401 || e.status === 403) {
				running = false;
				postMessage({ type: 'error', message: 'Gesture stream refused (HTTP ' + e.status + ')' });
				return;
			}
			if (!controller.signal.aborted) {
				console.error('Gesture stream error:', e);
			}
		}
		if (running && !controller.signal.aborted) {
			await new Promise((resolve) => setTimeout(resolve, retryDelay));
			retryDelay = Math.min(retryDelay * 2, 30000);
		}
	}
}

async function fetchStream(signal) {
	const fetchOptions = { signal };
	if (authToken) {
		fetchOptions.headers = {
			'Authorization': `Bearer ${authToken}`
		};
	}
	const response = await fetch('/gestures?rotation=' + rotation, fetchOptions);
	if (!response.ok) {
		const error = new Error('HTTP ' + response.status);
		error.status = response.status;
		throw error;
	}
	retryDelay = 1000;

	const reader = response.body.getReader();
	const decoder = new TextDecoder('utf-8');
	let buffer = '';

	while (true) {
		const { value, done } = await reader.read();
		if (done) break;

		buffer += decoder.decode(value, { stream: true });

		while (buffer.includes('\n')) {
			const index = buffer.indexOf('\n');
			const jsonStr = buffer.slice(0, index);
			buffer = buffer.slice(index + 1);

			try {
				const json = JSON.parse(jsonStr);
				const swipe = json.swipe || LEGACY_SWIPES[checkSwipeDirection(json)];
				if (swipe) {
					postMessage({ type: 'gesture', value: swipe });
				}
			} catch (e) {
				console.error('Error parsing JSON:', e);
			}
		}
	}
}


function checkSwipeDirection(json) {
	if (json.left > 400 && json.right < 100 && json.up < 100 && json.down < 100) {
		return 'left';
	} else if (json.right > 400 && json.left < 100 && json.up < 100 && json.down < 100) {
		return 'right';
	} else if (json.up > 400 && json.right < 100 && json.left < 100 && json.down < 100) {
		return 'up';
	} else if (json.down > 400 && json.right < 100 && json.up < 100 && json.left < 100) {
		return 'down';
	} else if (json.right > 600 && json.down > 600 && json.up < 50 && json.left < 50 ) {
		return 'topright-to-bottomleft'
	} else if (json.left > 600 && json.down > 600 && json.up < 50 && json.right < 50 ) {
		return 'topleft-to-bottomright'
	} else if (json.left > 600 && json.up > 600 && json.down < 50 && json.right < 50 ) {
		return 'bottomleft-to-topright'
	} else if (json.right > 600 && json.up > 600 && json.down < 50 && json.left < 50 ) {
		return 'bottomright-to-topleft'
	} else {
		return 'none';
	}
}
