# xAI Grok Imagine video plugin

This factory adapts NewAPI channel type 48 to xAI's asynchronous video API.
It accepts the OpenAI video and Responses protocols, submits to
`/v1/videos/generations`, polls `/v1/videos/{request_id}`, and exposes the
temporary MP4 URL as a credentialless task artifact. The plugin deliberately
reports only request facts (duration, aspect ratio, and resolution); channel
pricing remains configured by the deployment instead of being hard-coded here.

The factory is embedded in the binary; enable the `xai` factory in the task
plugin console/database before assigning a type-48 channel to a video model.
