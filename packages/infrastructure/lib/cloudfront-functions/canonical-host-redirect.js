// CloudFront Function (viewer-request) — 301-redirects any request that
// doesn't arrive on the canonical tankmaze.org host (e.g. the raw
// dXXXX.cloudfront.net domain) to https://tankmaze.org, preserving the
// path and query string. Requests already on tankmaze.org pass through
// unchanged to the S3 origin. See PRIORITIES.md item 199.
function handler(event) {
  var request = event.request;
  var host = request.headers.host && request.headers.host.value;

  if (host === 'tankmaze.org') {
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
