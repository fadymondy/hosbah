# Hosbah web — production Next.js (real `next build` + `next start`, not `next dev`).
# Building inside the container sidesteps the host Turbopack out-of-root symlink
# failure. Do NOT set NODE_ENV=production before `npm ci` — it drops devDependencies
# (tailwind/postcss) and the build fails. `next build` is production regardless.
FROM node:24-alpine
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1
# API_ORIGIN is where next's server-side rewrite sends /api.
ENV API_ORIGIN=http://api:8710
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build
ENV NODE_ENV=production
EXPOSE 3710
CMD ["npm", "run", "start"]
