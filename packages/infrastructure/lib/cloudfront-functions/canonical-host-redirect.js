// CloudFront Function (viewer-request), two responsibilities:
//
// 1. 301-redirects any request that doesn't arrive on the canonical
//    tankmaze.org host (e.g. the raw dXXXX.cloudfront.net domain) to
//    https://tankmaze.org, preserving the path and query string. See
//    PRIORITIES.md item 199.
//
// 2. Clean-URL rewrite (item 268): a handful of public routes are
//    prerendered as real static files at build time (e.g.
//    dist/leaderboard/index.html) so crawlers that don't execute JS see
//    genuine, distinct per-page content instead of the same SPA shell
//    repeated at every URL — the root cause of two straight AdSense
//    "low value content" rejections despite adding real in-app content,
//    since that content only ever rendered client-side. For any request
//    whose last path segment has no file extension (i.e. not a static
//    asset like /assets/foo.js or a top-level file like /robots.txt) and
//    doesn't already end in /index.html, rewrite the URI to
//    <path>/index.html before it reaches the S3 origin. Safe for routes
//    with no prerendered file too: S3 404s, and the distribution's
//    existing 404->200 /index.html error response (frontend-stack.ts)
//    still serves the SPA shell exactly as it did before this rewrite —
//    this only changes the outcome for paths that DO have a prerendered
//    file waiting.
function handler(event) {
  var request = event.request;
  var host = request.headers.host && request.headers.host.value;

  if (host === 'tankmaze.org') {
    var uri = request.uri;
    if (uri !== '/' && uri.slice(-11) !== '/index.html') {
      var lastSlash = uri.lastIndexOf('/');
      var lastSegment = uri.slice(lastSlash + 1);
      var hasExtension = lastSegment.indexOf('.') !== -1;
      if (!hasExtension) {
        request.uri = (uri.slice(-1) === '/' ? uri : uri + '/') + 'index.html';
      }
    }
    return request;
  }

  var qs = '';
  var params = request.querystring || {};
  var pairs = [];
  for (var key in params) {
    var param = params[key];
    if (param.multiValue) {
      for (var i = 0; i < param.multiValue.length; i++) {
        pairs.push(encodeURIComponent(key) + '=' + encodeURIComponent(param.multiValue[i].value));
      }
    } else {
      pairs.push(encodeURIComponent(key) + '=' + encodeURIComponent(param.value));
    }
  }
  if (pairs.length > 0) {
    qs = '?' + pairs.join('&');
  }

  return {
    statusCode: 301,
    statusDescription: 'Moved Permanently',
    headers: {
      location: { value: 'https://tankmaze.org' + request.uri + qs },
    },
  };
}
