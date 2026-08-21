### Security

- The `adapters/iris` module floors its indirect `github.com/sirupsen/logrus`
  dependency at 1.8.3. Iris pulls logrus in transitively — no ghtmx code
  calls it — and 1.8.3 is the release that fixes upstream's
  `logrus.Writer()` denial of service on single-line payloads larger
  than 64KB, so an application that does reach logrus through Iris is
  not held back by the adapter's module graph.
