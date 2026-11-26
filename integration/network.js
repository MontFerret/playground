async ({sockets, origin}) => {
  async function attempt(url) {
    let connection;
    try {
      connection = new WebSocket(url);
    } catch (error) {
      return error.name;
    }
    return new Promise(resolve => {
      connection.onopen = () => { connection.close(); resolve('opened'); };
      connection.onerror = () => resolve('error');
    });
  }

  async function run(urls) {
    return Promise.all(urls.map(attempt));
  }

  const iframe = document.createElement('iframe');
  document.body.append(iframe);
  const frame = iframe.contentWindow.eval(`(${run.toString()})`);
  iframe.contentWindow.eval(`globalThis.attempt = (${attempt.toString()})`);

  async function worker() {
    if (typeof Worker !== 'function') return {unavailable: 'Worker API absent'};
    const blob = new Blob([`${attempt.toString()}; ${run.toString()};
      postMessage('ready');
      onmessage = async ({data: [urls, origin]}) => {
        const [result] = await Promise.all([
          run(urls), fetch(origin + '/canary/worker-fetch', {mode: 'no-cors'}).catch(() => {}),
        ]);
        postMessage(result);
      };`], {type: 'text/javascript'});
    const url = URL.createObjectURL(blob);
    let dedicated;
    try {
      dedicated = new Worker(url);
      return await new Promise((resolve, reject) => {
        let ready = false;
        dedicated.onmessage = ({data}) => {
          if (data !== 'ready') return resolve(data);
          ready = true;
          dedicated.postMessage([sockets, origin]);
        };
        dedicated.onerror = error => {
          if (ready || error.message) return reject(new Error('Worker script failed'));
          resolve({unavailable: 'Worker script could not start'});
        };
      });
    } finally {
      if (dedicated) dedicated.terminate();
      URL.revokeObjectURL(url);
    }
  }

  function load(tag, route) {
    return new Promise(resolve => {
      const element = document.createElement(tag);
      element.onload = element.onerror = () => { element.remove(); resolve(); };
      element.src = origin + '/canary/' + route;
      document.body.append(element);
    });
  }

  const xhr = new Promise(resolve => {
    const request = new XMLHttpRequest();
    request.open('GET', origin + '/canary/xhr');
    request.onloadend = resolve;
    request.send();
  });
  const requests = Promise.all([
    fetch(origin + '/canary/fetch', {mode: 'no-cors'}).catch(() => {}),
    xhr, load('img', 'image'), load('script', 'script'), load('iframe', 'cross-origin-frame'),
  ]);
  const [main, framed, dedicated] = await Promise.all([run(sockets), frame(sockets), worker(), requests]);
  iframe.remove();
  return {main, frame: framed, worker: dedicated};
}
